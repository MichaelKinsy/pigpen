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
		// Only the session's own agents are this RPC's to stop; a nested child belongs to the agent waiting on it.
		// upstream: cross-extension-rpc.ts:178.
		if !isTopLevelAgent(r) {
			return nil, errors.New("Agent is owned by another agent or workflow")
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

// parentView is what a spawn needs from the session that asks for it: its system prompt (an `append` agent extends
// it), the environment probe, and the model and thinking level a child follows. A tool call of this extension's
// session reads them off its sdk.Context (viewOf); the nested tools take the calling session's own.
type parentView struct {
	SystemPrompt func() string
	Env          func(cwd string) envInfo
	Model        func() string
	Thinking     func() string
}

// startInput is one spawn. A top-level spawn names the type the caller asked for (the fallback policy resolves it);
// a nested one carries the owner and the registry of its branch's config root, and an already resolved type.
type startInput struct {
	Type, Prompt, Description, Name, Model, Thinking string
	MaxTurns                                         int
	Background, Isolated                             bool
	// Owner is set for a nested spawn: the agent that owns the child.
	Owner *owner
	// Registry is the nested spawn's agent registry (never the process-wide one); Type is then already resolved.
	Registry *agentRegistry
}

// viewOf is the parent view of a tool call: the call's own context for the prompt and the probe, and the session's
// latest context for the model and thinking level.
func (a *app) viewOf(ctx sdk.Context) parentView {
	return parentView{
		SystemPrompt: func() string { p, _ := ctx.GetSystemPrompt(); return p },
		Env:          func(cwd string) envInfo { return detectEnv(ctx, cwd) },
		Model: func() string {
			if parent, ok := a.context(); ok {
				return parent.ModelQualified()
			}
			return ""
		},
		Thinking: func() string {
			if parent, ok := a.context(); ok {
				if level, err := parent.GetThinkingLevel(); err == nil {
					return level
				}
			}
			return ""
		},
	}
}

// start builds the child's spec from the agent type and spawns it.
func (a *app) start(ctx sdk.Context, typ, prompt, description, name, model, thinking string, maxTurns int, background, isolated bool) (*agentRecord, error) {
	return a.startWith(a.viewOf(ctx), startInput{Type: typ, Prompt: prompt, Description: description, Name: name, Model: model, Thinking: thinking, MaxTurns: maxTurns, Background: background, Isolated: isolated})
}

func (a *app) startWith(view parentView, in startInput) (*agentRecord, error) {
	a.mu.Lock()
	cwd := a.cwd
	a.mu.Unlock()
	typ, prompt, description, name, model, thinking, maxTurns := in.Type, in.Prompt, in.Description, in.Name, in.Model, in.Thinking, in.MaxTurns
	var resolved string
	var cfg *agentConfig
	if in.Registry != nil {
		resolved, cfg = typ, in.Registry.get(typ)
	} else {
		res := resolveSpawnType(typ)
		if !res.OK {
			return nil, errors.New(res.Message)
		}
		resolved = res.Type
		cfg = getAgentConfig(resolved)
	}
	if cfg == nil {
		return nil, fmt.Errorf("Unknown agent type: %q", typ)
	}
	if feature := unportedAgentFeature(cfg); feature != "" {
		return nil, refusal(feature)
	}
	parentPrompt := ""
	if cfg.PromptMode == "append" {
		parentPrompt = view.SystemPrompt()
	}
	system := buildAgentPrompt(cfg, cwd, view.Env(cwd), parentPrompt)
	// `isolated` (the call's or the file's) means no extensions at all, so no nested tools; `extensions: false` alone
	// still gets the nested tools its file opted into. upstream: agent-runner.ts:855 (`!options.isolated`).
	explicitIsolation := in.Isolated || (cfg.Isolated != nil && *cfg.Isolated)
	spec := childSpec{Prompt: prompt, SystemPrompt: system, PromptMode: "replace", Cwd: cwd, Thinking: thinking, Tools: cfg.BuiltinToolNames, Isolated: explicitIsolation || cfg.Extensions == false, ConfigCwd: cwd}
	if !explicitIsolation {
		spec.Allowed = cfg.AllowedSubagents
	}
	if in.Owner == nil {
		spec.MaxDepth = getMaxSubagentDepth()
	}
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
	if spec.Model == "" {
		spec.Model = view.Model()
	}
	if spec.Thinking == "" {
		spec.Thinking = view.Thinking()
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
	return a.mgr.spawnOwned(resolved, description, name, spec, in.Background, in.Owner), nil
}
