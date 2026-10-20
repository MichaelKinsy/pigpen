package tintinweb_subagents

import (
	"errors"
	"fmt"
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

// rpcSpawn starts a background agent for another extension (pi-tasks' TaskExecute).
func (a *app) rpcSpawn(ctx sdk.Context, p map[string]any) (any, error) {
	if _, ok := a.context(); !ok {
		return nil, errors.New("No active session")
	}
	typ, _ := p["type"].(string)
	prompt, _ := p["prompt"].(string)
	opts, _ := p["options"].(map[string]any)
	id, err := a.spawnTopLevel(ctx, typ, prompt, opts)
	if err != nil {
		return nil, err
	}
	r := a.mgr.get(id)
	if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

// spawnTopLevel is the one door for a spawn that comes from outside the Agent tool (the RPC, the registry): the
// internal capabilities are dropped, an unsupported option is refused, the agent type goes through the same dispatch
// as the model's, and a model override is resolved and scope-checked like Agent({ model }). It returns the agent's id.
// upstream: index.ts spawnTopLevel and spawnResolved, cross-extension-rpc.ts (the model override).
func (a *app) spawnTopLevel(ctx sdk.Context, typ, prompt string, options map[string]any) (string, error) {
	opts := stripInternalSpawnOptions(options)
	if feature := unportedSpawnOption(opts); feature != "" {
		return "", refusal(feature)
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
			return "", errors.New(msg) // surfaced, so the caller sees the auth or typo problem instead of a silent fallback
		}
		// A model on the RPC payload is an orchestrator's choice, exactly like Agent({ model }): the hard error, never the warn.
		verdict := checkModelScope(modelScopeCheck{provider: str(found["provider"]), id: str(found["id"]), cwd: ctx.Cwd(), registry: reg,
			callerSupplied: true, agentLabel: typ, modelInput: override})
		if verdict.kind == "error" {
			return "", errors.New(verdict.message)
		}
		model = str(found["provider"]) + "/" + str(found["id"])
	}
	r, err := a.start(ctx, typ, prompt, description, model, thinking, maxTurns, false, spawnOptions{Name: name, Background: true})
	if err != nil {
		return "", err
	}
	return r.ID, nil
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

// start builds the child's spec from the agent type and spawns it. model is the override already resolved to
// "provider/id" ("" for none); the parent's model and thinking level are the last resort.
func (a *app) start(ctx sdk.Context, typ, prompt, description, model, thinking string, maxTurns *int, isolated bool, o spawnOptions) (*agentRecord, error) {
	a.mu.Lock()
	cwd := a.cwd
	a.mu.Unlock()
	res := resolveSpawnType(typ)
	if !res.OK {
		return nil, errors.New(res.Message)
	}
	resolved := res.Type
	cfg := getAgentConfig(resolved)
	if cfg == nil {
		return nil, fmt.Errorf("Unknown agent type: %q", typ)
	}
	if feature := unportedAgentFeature(cfg); feature != "" {
		return nil, refusal(feature)
	}
	parentPrompt := ""
	if cfg.PromptMode == "append" {
		parentPrompt, _ = ctx.GetSystemPrompt()
	}
	system := buildAgentPrompt(cfg, cwd, detectEnv(ctx, cwd), parentPrompt)
	spec := childSpec{Prompt: prompt, SystemPrompt: system, PromptMode: "replace", Cwd: cwd, Thinking: thinking, Tools: cfg.BuiltinToolNames, Isolated: isolated || (cfg.Isolated != nil && *cfg.Isolated) || cfg.Extensions == false}
	// The model: the override, else the agent file's (when it is exactly an available model), else the parent's.
	spec.Model = model
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
	case maxTurns != nil:
		spec.MaxTurns = *maxTurns
	case cfg.MaxTurns != nil:
		spec.MaxTurns = *cfg.MaxTurns
	default:
		if rt := a.runtime(); rt.defaultMaxTurns != nil {
			spec.MaxTurns = *rt.defaultMaxTurns
		}
	}
	spec.GraceTurns = a.runtime().graceTurns
	if description == "" {
		description = strings.Join(strings.Fields(prompt)[:min(5, len(strings.Fields(prompt)))], " ")
	}
	return a.mgr.spawnWith(resolved, description, spec, o), nil
}
