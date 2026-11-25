package agents

import (
	"errors"
	"fmt"
	"slices"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/eval"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/intent"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/jobs"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/lifecycle"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/schemaorder"
	"github.com/MichaelKinsy/pigpen/oh-my-pig/internal/urls"
)

// `history://` URL handlers, the `agent` and `workpool` eval preludes, and
// session lifecycle hooks on e.
//
// It performs no filesystem or network I/O at registration time: the initial
// `task` description uses the embedded bundled agents, and `session_start`
// refreshes the tool definition with project/user discovered agents.
func Register(e *sdk.Extension) {
	RegisterWithRuntime(e, defaultRuntime)
}

// RegisterWithRuntime wires an explicit Runtime onto e (used by Register and tests).
func RegisterWithRuntime(e *sdk.Extension, rt *Runtime) {
	if rt == nil {
		rt = defaultRuntime
	}
	// Install the single shared internal/jobs delivery listener so background
	// agent completions auto-deliver via SendMessage once without duplicates.
	jobs.EnsureDeliveryListener()

	// Register package-level session cleanup under "agents". Keyed by name, so
	// repeated Register calls are idempotent and always reach the shared Runtime.
	lifecycle.OnEnd("agents", func(sessionID string) {
		rt.EndSession(sessionID)
	})

	// Register `task` tool using embedded bundled agents (no I/O at registration time).
	registerTaskTool(e, rt, rt.Config, LoadBundledAgents(), "*")

	// Register `wait` tool.
	waitDef := schemaorder.Apply(waitToolDef(rt, false, false))
	e.RegisterTool(intent.Tool(waitDef))
	intent.RegisterSync(e, waitDef)
	// Register `agent://` and `history://` internal URL handlers.
	_ = urls.Register(&AgentURLHandler{Runtime: rt})
	_ = urls.Register(&HistoryURLHandler{Runtime: rt})

	// Register `agent` and `workpool` eval preludes (host-side only; omp's
	// embedded Python/JS preludes define the globals and route __agent__ /
	// __workpool__ directly to Invoke).
	eval.RegisterPrelude(rt.AgentPrelude())
	eval.RegisterPrelude(rt.WorkpoolPrelude())

	// On session_start: bind the long-lived session_start sdk.Context for
	// background children and job delivery, refresh `task` with live settings
	// and discovered project/user agents (respecting child session spawn policy
	// and effective host depth cap), and refresh `wait` with TUI/subagent wording.
	e.OnSessionStart(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sessID := rt.resolveSessionID(ctx); sessID != "" {
			rt.BindSessionContext(sessID, ctx)
			jobs.BindSession(sessID, ctx)
		}
		cfg := rt.EffectiveConfig(ctx)
		sessKey := rt.sessionKey(ctx)
		endTurn := isWaitEndTurn(ctx)

		if meta, isChild := rt.Registry.ChildMeta(sessKey); isChild {
			spawns := meta.Spawns
			policy := ResolveSpawnPolicy(spawns)
			effMax := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
			if policy.Enabled && CanSpawnAtDepth(effMax, meta.Depth) {
				rt.refreshTaskToolIfChanged(ctx, sessKey, cfg, spawns, true)
			}
			// Refresh wait with subagent clause when active in this child.
			if tools, err := ctx.GetActiveTools(); err != nil || slices.Contains(tools, "wait") {
				ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(waitToolDef(rt, true, false))))
			}
			return nil, nil
		}

		// Register Main ref for this top-level session so subagents can message Main immediately.
		artDir, _ := rt.sessionArtifactsDir(ctx)
		rt.ensureMainRef(sessKey, ctx, artDir)

		// Refresh `wait` with interactive TUI vs print wording.
		ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(waitToolDef(rt, false, endTurn))))

		rt.refreshTaskToolIfChanged(ctx, sessKey, cfg, "*", true)
		return nil, nil
	})

	// On before_agent_start: re-render the advertised `task` description/schema
	// whenever any task-advertising setting (task.batch, task.disabledAgents,
	// task.structured, task.steps, task.enableEffort, task.perCallModel,
	// task.isolation.enabled, async.enabled, or plan mode) changed mid-session
	// (e.g. via cfg:// writes), since internal/settings has no global OnChange hook.
	// upstream: packages/coding-agent/src/task/index.ts:810-875 (dynamic getters)
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if !rt.UseSettings {
			return nil, nil
		}
		cfg := rt.EffectiveConfig(ctx)
		sessKey := rt.sessionKey(ctx)
		if meta, isChild := rt.Registry.ChildMeta(sessKey); isChild {
			spawns := meta.Spawns
			policy := ResolveSpawnPolicy(spawns)
			effMax := EffectiveMaxRecursionDepth(cfg.MaxRecursionDepth)
			if policy.Enabled && CanSpawnAtDepth(effMax, meta.Depth) {
				rt.refreshTaskToolIfChanged(ctx, sessKey, cfg, spawns, false)
			}
			return nil, nil
		}
		rt.refreshTaskToolIfChanged(ctx, sessKey, cfg, "*", false)
		return nil, nil
	})

	// On session_shutdown: release ONLY the ending session via lifecycle.End(sessionID).
	// Note: session_before_switch is cancellable (a handler may return {cancel: true}),
	// so cleanup runs only on session_shutdown which the host emits in teardownCurrent.
	e.OnSessionShutdown(func(ctx sdk.Context, _ map[string]any) (any, error) {
		if sessID := rt.resolveSessionID(ctx); sessID != "" {
			lifecycle.End(sessID)
		}
		return nil, nil
	})
}

// taskToolAdvertisedSignature computes a deterministic fingerprint of every
// setting that affects the advertised `task` tool description or JSON schema.
func taskToolAdvertisedSignature(cfg Config, spawns string) string {
	disabled := append([]string(nil), cfg.DisabledAgents...)
	slices.Sort(disabled)
	return fmt.Sprintf(
		"async=%t|batch=%t|struct=%t|steps=%t|effort=%t|iso=%t|pcm=%s|spawns=%s|disabled=%v",
		cfg.AsyncEnabled, cfg.BatchEnabled, cfg.Structured, cfg.StepsEnabled,
		cfg.EnableEffort, cfg.IsolationEnabled, cfg.PerCallModel, spawns, disabled,
	)
}

// refreshTaskToolIfChanged re-registers the `task` tool on ctx when force is
// true or when any setting affecting `task`'s description or schema changed
// since the last registration for sessKey.
func (rt *Runtime) refreshTaskToolIfChanged(ctx sdk.Context, sessKey string, cfg Config, spawns string, force bool) {
	sig := taskToolAdvertisedSignature(cfg, spawns)
	rt.mu.Lock()
	prev, ok := rt.taskToolSigs[sessKey]
	if !force && ok && prev == sig {
		rt.mu.Unlock()
		return
	}
	rt.taskToolSigs[sessKey] = sig
	rt.mu.Unlock()

	disc := rt.discover(ctx.Cwd())
	if len(disc.Agents) > 0 {
		ctx.RegisterTool(intent.ToolForContext(ctx, schemaorder.Apply(taskToolDef(rt, cfg, disc.Agents, spawns))))
	}
}

func registerTaskTool(e *sdk.Extension, rt *Runtime, cfg Config, agents []AgentDefinition, spawns string) {
	def := schemaorder.Apply(taskToolDef(rt, cfg, agents, spawns))
	e.RegisterTool(intent.Tool(def))
	intent.RegisterSync(e, def)
}

func waitToolDef(rt *Runtime, subagent, endTurn bool) sdk.ToolDefinition {
	return sdk.ToolDefinition{
		Name:          "wait",
		Label:         "Wait",
		Description:   RenderWaitDescription(rt.Config.WaitTopMs, subagent, endTurn),
		Parameters:    WaitSchema(),
		ExecutionMode: "sequential",
		Execute: func(ctx sdk.Context, _ map[string]any) (any, error) {
			return rt.ExecuteWait(ctx)
		},
	}
}

func taskToolDef(rt *Runtime, cfg Config, agents []AgentDefinition, spawns string) sdk.ToolDefinition {
	policy := ResolveSpawnPolicy(spawns)
	defaultAgent := policy.DefaultAgent
	if defaultAgent == "" {
		defaultAgent = DefaultSpawnAgent
	}
	return sdk.ToolDefinition{
		Name:          "task",
		Label:         "Task",
		Description:   RenderTaskDescription(cfg, agents, spawns),
		Parameters:    TaskSchema(cfg, defaultAgent),
		ExecutionMode: "parallel",
		// Match omp's lenient task validation (`task/index.ts:748`, `task/types.ts:67,144-150`,
		// `packages/agent/src/tool-arguments.ts:29-42`):
		// - When arguments pass the active wire schema, apply omp's arktype default
		//   `agent = defaultAgent` (`task/types.ts:67,147,178,198,218,231`) and
		//   repair double-encoded natural-language fields.
		// - When arguments fail the active wire schema, PiG's host validator would
		//   otherwise refuse them in PiG wording (or coerce `context: null` -> `"null"`)
		//   before ExecuteTask runs; return omp's own `validateShapeParams` /
		//   `validateSpawnParams` error directly (which PiG surfaces verbatim:
		//   `agent/tool_execution.go:377-381`), and never mutate raw fallback args.
		PrepareArguments: func(params map[string]any) (map[string]any, error) {
			return prepareTaskArguments(cfg, defaultAgent, params)
		},
		Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
			return rt.ExecuteTask(ctx, params)
		},
	}
}

// prepareTaskArguments implements omp's `task` argument preparation after
// `internal/intent`'s lenient validation pass:
//   - Schema-valid calls receive omp's arktype `agent` default (`task/types.ts:144-239`)
//     and double-encoded string repair (`task-repair-args.ts:111`).
//   - Schema-invalid calls (raw fallback from `internal/intent`) are checked with
//     omp's `ValidateTaskParams` (`task/index.ts:222-336`) and refused in omp's
//     exact wording so PiG's host validator never answers in PiG wording or
//     coerces `null` into `"null"`. Any remaining raw fallback call that passes
//     `ValidateTaskParams` is returned untouched.
func prepareTaskArguments(cfg Config, defaultAgent string, params map[string]any) (map[string]any, error) {
	if params == nil {
		if msg := ValidateTaskParams(cfg, DecodeTaskParams(map[string]any{})); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, nil
	}

	if !taskParamsMatchWireSchema(cfg, params) {
		// Raw fallback path (`packages/agent/src/tool-arguments.ts:32-41`):
		// refuse shapes omp's execute() rejects with omp's exact error text;
		// otherwise return the raw arguments unchanged.
		if msg := ValidateTaskParams(cfg, RepairTaskParams(DecodeTaskParams(params))); msg != "" {
			return nil, errors.New(msg)
		}
		return params, nil
	}

	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
	}
	if cfg.BatchEnabled {
		if s, ok := out["context"].(string); ok {
			out["context"] = RepairDoubleEncodedJSONString(s)
		}
		if list, ok := out["tasks"].([]any); ok {
			repaired := make([]any, len(list))
			for i, raw := range list {
				m, ok := raw.(map[string]any)
				if !ok {
					repaired[i] = raw
					continue
				}
				cp := make(map[string]any, len(m)+1)
				for k, v := range m {
					cp[k] = v
				}
				if _, hasAgent := cp["agent"]; !hasAgent && defaultAgent != "" {
					cp["agent"] = defaultAgent
				}
				if s, ok := cp["task"].(string); ok {
					cp["task"] = RepairDoubleEncodedJSONString(s)
				}
				repaired[i] = cp
			}
			out["tasks"] = repaired
		}
	} else {
		if _, hasAgent := out["agent"]; !hasAgent && defaultAgent != "" {
			out["agent"] = defaultAgent
		}
		if s, ok := out["task"].(string); ok {
			out["task"] = RepairDoubleEncodedJSONString(s)
		}
	}
	return out, nil
}

// taskParamsMatchWireSchema reports whether params (after intent's lenient
// validation pass) conforms to the active `task` wire schema under cfg.
// When true, intent's validator already succeeded and normalized params
// (unknown nested keys stripped, types matched); when false, intent fell back
// to raw arguments and PiG's host validator would otherwise reject or coerce them.
func taskParamsMatchWireSchema(cfg Config, params map[string]any) bool {
	if params == nil {
		return false
	}
	if cfg.BatchEnabled {
		if _, ok := params["context"].(string); !ok {
			return false
		}
		rawTasks, ok := params["tasks"].([]any)
		if !ok {
			return false
		}
		// Batch schema also rejects top-level `agent` (`task/types.ts:108-118`,
		// `guardBatchAgent`) and stale `schema` / `model` / `task` / `steps`.
		if _, hasAgent := params["agent"]; hasAgent {
			return false
		}
		if _, hasSchema := params["schema"]; hasSchema {
			return false
		}
		for _, raw := range rawTasks {
			m, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			if !taskItemMatchesWireSchema(cfg, m) {
				return false
			}
		}
		return true
	}
	return taskItemMatchesWireSchema(cfg, params)
}

func taskItemMatchesWireSchema(cfg Config, m map[string]any) bool {
	if _, ok := m["solutionSpace"].(string); !ok {
		return false
	}
	if cfg.StepsEnabled {
		rawSteps, ok := m["steps"].([]any)
		if !ok {
			return false
		}
		for _, s := range rawSteps {
			if _, ok := s.(string); !ok {
				return false
			}
		}
	} else {
		if _, ok := m["task"].(string); !ok {
			return false
		}
	}
	if v, has := m["name"]; has {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	if v, has := m["agent"]; has {
		if _, ok := v.(string); !ok {
			return false
		}
	}
	if cfg.EnableEffort {
		if v, has := m["effort"]; has {
			s, ok := v.(string)
			if !ok || (s != "lo" && s != "med" && s != "hi") {
				return false
			}
		}
	}
	if cfg.Structured {
		if v, has := m["schemaMode"]; has {
			s, ok := v.(string)
			if !ok || (s != "permissive" && s != "strict") {
				return false
			}
		}
	}
	if cfg.EvalToolsEnabled {
		if v, has := m["tools"]; has {
			arr, ok := v.([]any)
			if !ok {
				return false
			}
			for _, item := range arr {
				if _, ok := item.(string); !ok {
					return false
				}
			}
		}
	}
	if cfg.PerCallModel == "on" {
		if v, has := m["model"]; has {
			if _, ok := v.(string); !ok {
				return false
			}
		}
	}
	if cfg.IsolationEnabled {
		if v, has := m["isolated"]; has {
			if _, ok := v.(bool); !ok {
				return false
			}
		}
	}
	return true
}

// ResetForTests clears package-global runtime state between tests.
func ResetForTests() {
	defaultRuntime.cancelAllForTests()
	defaultRuntime = newDefaultRuntime()
	ResetTaskNames()
}

// Keep fmt imported even if refactored later.
var _ = fmt.Sprintf
