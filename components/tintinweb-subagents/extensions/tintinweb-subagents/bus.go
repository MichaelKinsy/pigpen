package tintinweb_subagents

import (
	"errors"
	"slices"
	"strings"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The cross-extension protocol over the event bus, version 2: ping, spawn, stop and consume, each answered on
// `<channel>:reply:<requestId>` with {success, data|error}. upstream: src/cross-extension-rpc.ts.

// registerRPC wires the RPC listeners and returns their unsubscribe functions. They are registered once the session
// has started, not at load: a session that filtered this extension out must not advertise an RPC it cannot answer.
// upstream: cross-extension-rpc.ts registerRpcHandlers, index.ts session_start (#142).
func (a *app) registerRPC(bus sdk.EventBus) []func() {
	var offs []func()
	handle := func(channel string, fn func(ctx sdk.Context, params map[string]any) (any, error)) {
		off, _ := bus.On(channel, func(ctx sdk.Context, raw any) error {
			params, _ := raw.(map[string]any)
			requestID, _ := params["requestId"].(string)
			reply := map[string]any{"success": true}
			data, err := fn(ctx, params)
			if err != nil {
				reply = map[string]any{"success": false, "error": err.Error()}
			} else if data != nil {
				reply["data"] = data
			}
			return ctx.Events().Emit(channel+":reply:"+requestID, reply)
		})
		offs = append(offs, off)
	}
	handle("subagents:rpc:ping", func(sdk.Context, map[string]any) (any, error) {
		return map[string]any{"version": protocolVersion}, nil
	})
	handle("subagents:rpc:spawn", a.rpcSpawn)
	handle("subagents:rpc:stop", func(_ sdk.Context, p map[string]any) (any, error) {
		id, _ := p["agentId"].(string)
		r := a.mgr.get(id)
		if r == nil {
			return nil, errors.New("Agent not found")
		}
		// Only the session's own agents are this RPC's to stop: an owned child is waited on by its owner.
		if !r.topLevel() {
			return nil, errors.New("Agent is owned by another agent or workflow")
		}
		if !a.mgr.abort(r) {
			return nil, errors.New("Agent is not running")
		}
		return nil, nil
	})
	handle("subagents:rpc:consume", func(_ sdk.Context, p map[string]any) (any, error) {
		id, _ := p["agentId"].(string)
		r := a.mgr.resolve(id)
		// Like get_subagent_result: a running agent has no result to consume, and its notification is the caller's only
		// signal that it finished.
		if r == nil || !r.topLevel() || !a.mgr.consume(r.ID) {
			return nil, errors.New("Agent not found or still running")
		}
		a.cancelNudge(r.ID)
		return nil, nil
	})
	return offs
}

// rpcSpawn starts a background agent for another extension (pi-tasks' TaskExecute). The options a caller cannot be
// trusted with are dropped, as in the original: they name a file to open and replay, a handle to take over, an owner
// or a pool to be charged to. upstream: index.ts spawnTopLevel.
func (a *app) rpcSpawn(ctx sdk.Context, p map[string]any) (any, error) {
	if _, ok := a.context(); !ok {
		return nil, errors.New("No active session")
	}
	typ, _ := p["type"].(string)
	prompt, _ := p["prompt"].(string)
	opts, _ := p["options"].(map[string]any)
	r, err := a.spawnTopLevel(ctx, typ, prompt, opts)
	if err != nil {
		return nil, err
	}
	if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
		return nil, err
	}
	// With isolation the agent is not running yet: the repo copy is a git call, and a strict-isolation failure fails
	// the spawn. upstream: agent-manager.ts awaitStartup.
	if err := a.mgr.awaitSetup(ctx.Done(), r); err != nil {
		return nil, err
	}
	return map[string]any{"id": r.ID}, nil
}

// spawnTopLevel is the one door for a spawn that comes from outside the Agent tool (the RPC, the registry): the
// internal capabilities are dropped, an unsupported option is refused, the agent type goes through the same dispatch
// as the model's, and a model override is resolved and scope-checked like Agent({ model }). It returns the agent's record.
// upstream: index.ts spawnTopLevel and spawnResolved, cross-extension-rpc.ts (the model override).
func (a *app) spawnTopLevel(ctx sdk.Context, typ, prompt string, options map[string]any) (*agentRecord, error) {
	a.reregisterAgents() // an agent file added mid-session is spawnable here too. upstream: index.ts spawnResolved reloadCustomAgents.
	opts := stripInternalSpawnOptions(options)
	if feature := unportedSpawnOption(opts); feature != "" {
		return nil, refusal(feature)
	}
	description, _ := opts["description"].(string)
	name, _ := opts["name"].(string)
	thinking, _ := opts["thinkingLevel"].(string)
	var maxTurns *int
	if f, ok := opts["maxTurns"].(float64); ok && f > 0 {
		n := int(f)
		maxTurns = &n
	}
	// A null model reads as "inherit", not as an override to resolve.
	model := ""
	if override, ok := opts["model"].(string); ok && override != "" {
		reg := registryOf(ctx)
		found, msg := resolveModel(override, reg)
		if msg != "" {
			return nil, errors.New(msg) // surfaced, so the caller sees the auth or typo problem instead of a silent fallback
		}
		// A model on the RPC payload is an orchestrator's choice, exactly like Agent({ model }): the hard error, never the warn.
		verdict := checkModelScope(modelScopeCheck{provider: str(found["provider"]), id: str(found["id"]), cwd: ctx.Cwd(), registry: reg,
			callerSupplied: true, agentLabel: typ, modelInput: override})
		if verdict.kind == "error" {
			return nil, errors.New(verdict.message)
		}
		model = str(found["provider"]) + "/" + str(found["id"])
	}
	// Like the original, only isBackground: true makes the spawn a pooled background one; absent or false is detached,
	// holds no pool slot and is not announced to the model (agent-manager.ts:520-540, :969).
	background, _ := opts["isBackground"].(bool)
	// The original takes these as truthy values, not as booleans.
	isolated, inherit, bypass := jsTruthy(opts["isolated"]), jsTruthy(opts["inheritContext"]), jsTruthy(opts["bypassQueue"])
	// The option goes straight to the manager: it skips the resolver, the manager's own switch gates it.
	isolation, _ := opts["isolation"].(string)
	r, err := a.start(ctx, startRequest{Type: typ, Prompt: prompt, Description: description, Model: model, Thinking: thinking, MaxTurns: maxTurns,
		Isolated: isolated, InheritContext: inherit, Worktree: isolation == "worktree", Spawn: spawnOptions{Name: name, Background: background, BypassQueue: bypass}})
	if err != nil {
		return nil, err
	}
	return r, nil
}

// resolveDefaultModel is the model an agent runs when nothing overrides it: its file's `model:` when that is exactly
// an available model, else the parent's. upstream: agent-runner.ts resolveDefaultModel.
func resolveDefaultModel(parent string, reg modelRegistry, configModel string) string {
	if slash := strings.Index(configModel, "/"); slash != -1 {
		provider, id := configModel[:slash], configModel[slash+1:]
		found := reg.Find(provider, id)
		if found != nil && (reg.GetAvailable == nil || findModel(reg.GetAvailable(), provider, id) != nil) {
			return provider + "/" + id
		}
	}
	return parent
}

// startRequest is one spawn: what the Agent tool or the spawn RPC asked for, already resolved against the agent
// file where the original resolves it (isolated and inherit_context in the Agent tool; the RPC states them itself).
// Model is the override already resolved to "provider/id" ("" for none); the parent's model and thinking level are
// the last resort.
type startRequest struct {
	Type, Prompt, Description, Model, Thinking string
	MaxTurns                                   *int
	Isolated, InheritContext                   bool
	Worktree                                   bool // run in a temporary git worktree (isolation: "worktree")
	// Registry is a nested spawn's agent registry (its branch's config root, never the process-wide one); Type is then
	// already resolved. ConfigCwd is that root.
	Registry  *agentRegistry
	ConfigCwd string
	Spawn     spawnOptions // the name, the pool and the signal of the spawn
	// Per-call settings that only the Agent tool has (the RPC spawn path takes none).
	Transcript bool // write a `.output` transcript (the Agent tool's decision: the agent file, else the default)
	SessionID  string
}

// toolNamesOf is the built-in tools an agent type asks for: its file's list, else all of them.
func toolNamesOf(cfg *agentConfig) []string {
	if cfg.BuiltinToolNames != nil {
		return cfg.BuiltinToolNames
	}
	return slices.Clone(builtinToolNames)
}

// start builds the child's spec from the agent type and spawns it.
func (a *app) start(ctx sdk.Context, q startRequest) (*agentRecord, error) {
	a.mu.Lock()
	cwd, remember := a.cwd, a.rememberAgents
	a.mu.Unlock()
	// A nested spawn names a type its parent's branch registry already resolved; a top-level one goes through the
	// fallback policy and the process-wide registry.
	var resolved string
	var cfg *agentConfig
	if q.Registry != nil {
		resolved, cfg = q.Type, q.Registry.get(q.Type)
	} else {
		res := resolveSpawnType(q.Type)
		if !res.OK {
			return nil, errors.New(res.Message)
		}
		resolved = res.Type
		cfg = getAgentConfig(resolved)
	}
	if cfg == nil {
		// The unset fallback names general-purpose even when it was removed from the registry: run the built-in one under
		// the resolved name. upstream: agent-runner.ts runAgent (DEFAULT_AGENTS fallback).
		fallback := *defaultAgents().get("general-purpose")
		fallback.Name = resolved
		cfg = &fallback
	}
	parentPrompt := ""
	if cfg.PromptMode == "append" {
		parentPrompt, _ = ctx.GetSystemPrompt()
	}
	// isolated overrides the agent file's extensions and skills.
	skills := cfg.Skills
	if q.Isolated {
		skills = false
	}
	var skillBlocks []preloadedSkill
	if names, ok := skills.([]string); ok {
		skillBlocks = preloadSkills(names, cwd)
	}
	// Persistent memory adds the tools it needs to the agent's list before the scope is planned. upstream: agent-runner.ts runAgent.
	toolNames := getToolNamesForType(resolved)
	if q.Registry != nil {
		toolNames = toolNamesOf(cfg)
	}
	memoryWrites := false
	if cfg.Memory != "" {
		toolNames, memoryWrites = memoryTools(cfg, toolNames)
	}
	// Nested delegation: an agent whose file says `allowed_subagents` gets the nested tools (which this extension
	// registers in the child), unless it is isolated or at the depth cap. The tools are re-admitted past the scope that
	// keeps this extension's own tools out of a child, unless `disallowed_tools` takes them back. upstream: agent-runner.ts:843-866.
	nested := q.Spawn.ParentAgentID != ""
	depth, maxDepth := max(1, q.Spawn.Depth), q.Spawn.MaxSubagentDepth
	if !nested { // a top-level agent's branch is capped by the setting; a caller cannot name another cap
		q.Spawn.Depth, maxDepth = 0, getMaxSubagentDepth()
		q.Spawn.MaxSubagentDepth = maxDepth
	}
	var allowed any
	var readmit []string
	if !q.Isolated && cfg.AllowedSubagents != nil && depth < min(maxDepth, subagentDepthCeiling) {
		allowed = cfg.AllowedSubagents
		for _, name := range nestedToolNames {
			if !slices.Contains(cfg.DisallowedTools, name) {
				readmit = append(readmit, name)
			}
		}
	}
	plan, scope, err := a.childScope(ctx, resolved, cfg, q.Isolated, toolNames, cwd, readmit)
	if err != nil {
		return nil, err
	}
	// The environment of an agent that runs in this workspace is read now; one in a worktree copy is read once the
	// copy exists (it is another directory).
	var eagerEnv envInfo
	if !q.Worktree {
		eagerEnv = detectEnv(ctx, cwd)
	}
	spec := childSpec{Prompt: q.Prompt, PromptMode: "replace", Cwd: cwd, Thinking: q.Thinking, Worktree: q.Worktree,
		NoExtensions: scope.NoExtensions, ExtensionPaths: scope.ExtensionPaths, AllowTools: scope.AllowTools, ExcludeTools: scope.ExcludeTools,
		NoSkills: skills != true, Warnings: plan.Warnings, Transcript: q.Transcript, SessionID: q.SessionID,
		Depth: depth, MaxDepth: maxDepth, Allowed: allowed, ConfigCwd: cwd}
	if q.ConfigCwd != "" {
		spec.ConfigCwd = q.ConfigCwd
	}
	if spec.nestedActive() {
		// Every transcript of the branch goes under the root session, whatever agent wrote it.
		spec.RootSessionID = q.SessionID
		if spec.RootSessionID == "" {
			spec.RootSessionID, _ = a.sessionIDOf(ctx)
		}
	}
	spec.Prepare = func(spec *childSpec, workCwd, worktreeBase string) error {
		env := eagerEnv
		if q.Worktree {
			env = detectEnvVia(a.mgr.git, workCwd)
		}
		extras := promptExtras{SkillBlocks: skillBlocks, WorktreeBase: worktreeBase}
		if cfg.Memory != "" {
			var err error
			if extras.MemoryBlock, err = memoryBlockFor(cfg, memoryWrites, workCwd); err != nil {
				return err
			}
		}
		spec.SystemPrompt = buildAgentPrompt(cfg, workCwd, env, parentPrompt, extras)
		return nil
	}
	if trusted, err := ctx.IsProjectTrusted(); err == nil {
		spec.Trust = "no-approve"
		if trusted {
			spec.Trust = "approve"
		}
	}
	if sessionPersisted(cfg, nested, remember) {
		spec.Persist = true
		spec.SessionDir = resolveConfiguredSessionDir(cfg.SessionDir, cwd)
		if f, err := ctx.GetSessionFile(); err == nil && f != nil {
			spec.ParentSession = *f
		}
	}
	if q.InheritContext {
		// The conversation is read when the run starts, not now: an agent queued at the concurrency limit sees what was
		// said while it waited. upstream: agent-runner.ts:1101-1105.
		spec.InheritContext = func() (string, error) { return parentContext(ctx) }
	}
	spec.SessionName = cfg.Name
	// The model: the override, else the agent file's (when it is exactly an available model), else the parent's.
	spec.Model = q.Model
	if spec.Model == "" {
		spec.Model = resolveDefaultModel(ctx.ModelQualified(), registryOf(ctx), cfg.Model)
	}
	if spec.Thinking == "" {
		spec.Thinking = cfg.Thinking
	}
	// Without a thinking level of its own the child follows the parent's current one.
	if parent, ok := a.context(); ok && spec.Thinking == "" {
		if level, err := parent.GetThinkingLevel(); err == nil {
			spec.Thinking = level
		}
	}
	switch {
	case q.MaxTurns != nil:
		spec.MaxTurns = *q.MaxTurns
	case cfg.MaxTurns != nil:
		spec.MaxTurns = *cfg.MaxTurns
	default:
		if rt := a.runtime(); rt.defaultMaxTurns != nil {
			spec.MaxTurns = *rt.defaultMaxTurns
		}
	}
	spec.GraceTurns = a.runtime().graceTurns
	description := q.Description
	if description == "" {
		description = strings.Join(strings.Fields(q.Prompt)[:min(5, len(strings.Fields(q.Prompt)))], " ")
	}
	return a.mgr.spawnWith(resolved, description, spec, q.Spawn), nil
}

// jsTruthy is JS truthiness of a decoded JSON value: false, 0, "" and null are falsy; anything else, including an empty
// array or object, is truthy.
func jsTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return true
}
