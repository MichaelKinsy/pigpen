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

func (a *app) registerRPC(bus sdk.EventBus) {
	handle := func(channel string, fn func(ctx sdk.Context, params map[string]any) (any, error)) {
		_, _ = bus.On(channel, func(ctx sdk.Context, raw any) error {
			params, _ := raw.(map[string]any)
			requestID, _ := params["requestId"].(string)
			reply := map[string]any{"success": true}
			var data any
			var err error
			if perr := catch("rpc "+channel, func() { data, err = fn(ctx, params) }); perr != nil {
				err = perr
			}
			if err != nil {
				reply = map[string]any{"success": false, "error": err.Error()}
			} else if data != nil {
				reply["data"] = data
			}
			return ctx.Events().Emit(channel+":reply:"+requestID, reply)
		})
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
		if !a.mgr.abort(r) {
			return nil, errors.New("Agent is not running")
		}
		return nil, nil
	})
	handle("subagents:rpc:consume", func(_ sdk.Context, p map[string]any) (any, error) {
		id, _ := p["agentId"].(string)
		if !a.mgr.consume(id) {
			return nil, errors.New("Agent not found or still running")
		}
		return nil, nil
	})
}

// rpcSpawn starts a background agent for another extension (pi-tasks' TaskExecute).
func (a *app) rpcSpawn(ctx sdk.Context, p map[string]any) (any, error) {
	if _, ok := a.context(); !ok {
		return nil, errors.New("No active session")
	}
	typ, _ := p["type"].(string)
	prompt, _ := p["prompt"].(string)
	opts, _ := p["options"].(map[string]any)
	if feature := unportedSpawnOption(opts); feature != "" {
		return nil, refusal(feature)
	}
	description, _ := opts["description"].(string)
	model, _ := opts["model"].(string)
	name, _ := opts["name"].(string)
	thinking, _ := opts["thinkingLevel"].(string)
	var maxTurns int
	if f, ok := opts["maxTurns"].(float64); ok {
		maxTurns = int(f)
	}
	r, err := a.start(ctx, typ, prompt, description, name, model, thinking, maxTurns, true, false)
	if err != nil {
		return nil, err
	}
	if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
		return nil, err
	}
	return map[string]any{"id": r.ID}, nil
}

// start builds the child's spec from the agent type and spawns it.
func (a *app) start(ctx sdk.Context, typ, prompt, description, name, model, thinking string, maxTurns int, background, isolated bool, options ...func(*agentRecord)) (*agentRecord, error) {
	return a.startOwned(ctx, "", typ, prompt, description, name, model, thinking, maxTurns, background, isolated, options...)
}

// startOwned is start for an agent a workflow run owns (owner is the run id).
func (a *app) startOwned(ctx sdk.Context, owner, typ, prompt, description, name, model, thinking string, maxTurns int, background, isolated bool, options ...func(*agentRecord)) (*agentRecord, error) {
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
		return nil, &workflowRefusalError{feature}
	}
	parentPrompt := ""
	if cfg.PromptMode == "append" {
		parentPrompt, _ = ctx.GetSystemPrompt()
	}
	env := detectEnv(ctx, cwd)
	system := buildAgentPrompt(cfg, cwd, env, parentPrompt)
	if owner != "" {
		system = withWorkflowChildBlock(system, cfg, cwd, env)
	}
	spec := childSpec{Prompt: prompt, SystemPrompt: system, PromptMode: "replace", Cwd: cwd, Tools: cfg.BuiltinToolNames, Isolated: isolated || (cfg.Isolated != nil && *cfg.Isolated) || cfg.Extensions == false}
	// An agent file's model, thinking level and turn limit outrank the call's, which only fills gaps; the original's
	// haiku default of Explore falls back to the parent's model when it is not available, so this port never forces it.
	// upstream: invocation-config.ts resolveAgentInvocationConfig.
	pinned := cfg
	if cfg.IsDefault && cfg.Name == "Explore" {
		c := *cfg
		c.Model = ""
		pinned = &c
	}
	inv := resolveInvocation(pinned, model, thinking, maxTurns)
	spec.Model, spec.Thinking = inv.modelInput, inv.thinking
	// Without a model or thinking level of its own the child follows the parent's current ones.
	if parent, ok := a.context(); ok {
		if spec.Model == "" {
			spec.Model = parent.ModelQualified()
		}
		if spec.Thinking == "" {
			if level, err := parent.GetThinkingLevel(); err == nil {
				spec.Thinking = level
			}
		}
	}
	if inv.maxTurns != nil {
		spec.MaxTurns = *inv.maxTurns
	} else {
		a.mu.Lock()
		if a.defaultMaxTurns != nil {
			spec.MaxTurns = *a.defaultMaxTurns
		}
		a.mu.Unlock()
	}
	a.mu.Lock()
	spec.GraceTurns = a.graceTurns
	a.mu.Unlock()
	if description == "" {
		description = strings.Join(strings.Fields(prompt)[:min(5, len(strings.Fields(prompt)))], " ")
	}
	snapshot := buildInvocation(ctx, inv, background, isolated || (cfg.Isolated != nil && *cfg.Isolated))
	obs := a.ui.newObserver(snapshot, spec.MaxTurns)
	spec.Observer = obs
	r := a.mgr.spawnOwned(owner, resolved, description, name, spec, background, options...)
	a.ui.attach(r, obs)
	return r, nil
}
