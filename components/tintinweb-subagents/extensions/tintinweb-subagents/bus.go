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
			data, err := fn(ctx, params)
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
	description, _ := opts["description"].(string)
	model, _ := opts["model"].(string)
	var maxTurns int
	if f, ok := opts["maxTurns"].(float64); ok {
		maxTurns = int(f)
	}
	r, err := a.start(ctx, typ, prompt, description, "", model, "", maxTurns, true, false)
	if err != nil {
		return nil, err
	}
	if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
		return nil, err
	}
	return map[string]any{"id": r.ID}, nil
}

// start builds the child's spec from the agent type and spawns it.
func (a *app) start(ctx sdk.Context, typ, prompt, description, name, model, thinking string, maxTurns int, background, isolated bool) (*agentRecord, error) {
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
	parentPrompt := ""
	if cfg.PromptMode == "append" {
		parentPrompt, _ = ctx.GetSystemPrompt()
	}
	system := buildAgentPrompt(cfg, cwd, detectEnv(ctx, cwd), parentPrompt)
	spec := childSpec{Prompt: prompt, SystemPrompt: system, PromptMode: "replace", Cwd: cwd, Thinking: thinking, Tools: cfg.BuiltinToolNames, Isolated: isolated || (cfg.Isolated != nil && *cfg.Isolated) || cfg.Extensions == false}
	if cfg.IsDefault && cfg.Name == "Explore" {
		spec.Model = "" // the original's haiku default falls back to the parent's model when it is not available; this port never forces it
	} else {
		spec.Model = cfg.Model
	}
	if model != "" {
		spec.Model = model
	}
	if spec.Thinking == "" {
		spec.Thinking = cfg.Thinking
	}
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
	switch {
	case maxTurns > 0:
		spec.MaxTurns = maxTurns
	case cfg.MaxTurns != nil:
		spec.MaxTurns = *cfg.MaxTurns
	default:
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
	return a.mgr.spawn(resolved, description, name, spec, background), nil
}
