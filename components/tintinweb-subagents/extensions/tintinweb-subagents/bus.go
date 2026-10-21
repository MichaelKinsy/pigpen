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
	// The original takes these as truthy values, not as booleans.
	isolated, inherit, bypass := jsTruthy(opts["isolated"]), jsTruthy(opts["inheritContext"]), jsTruthy(opts["bypassQueue"])
	r, err := a.start(ctx, startRequest{Type: typ, Prompt: prompt, Description: description, Name: name, Model: model, Thinking: thinking,
		MaxTurns: maxTurns, Background: true, Isolated: isolated, InheritContext: inherit, BypassQueue: bypass})
	if err != nil {
		return nil, err
	}
	if err := a.mgr.awaitStartup(r, 30*time.Second); err != nil {
		return nil, err
	}
	return map[string]any{"id": r.ID}, nil
}

// startRequest is one spawn: what the Agent tool or the spawn RPC asked for, already resolved against the agent
// file where the original resolves it (isolated and inherit_context in the Agent tool; the RPC states them itself).
type startRequest struct {
	Type, Prompt, Description, Name, Model, Thinking string
	MaxTurns                                         int
	Background                                       bool
	Isolated, InheritContext                         bool
	BypassQueue                                      bool
	// ResumeFile and Reclaim reopen an evicted agent's conversation under the names it held. Set only by the mention
	// dispatcher, from a tombstone this extension wrote; the spawn RPC drops both. upstream: spawnTopLevel.
	ResumeFile string
	Reclaim    *reclaim
}

// start builds the child's spec from the agent type and spawns it.
func (a *app) start(ctx sdk.Context, q startRequest) (*agentRecord, error) {
	a.mu.Lock()
	cwd, remember := a.cwd, a.rememberAgents
	a.mu.Unlock()
	res := resolveSpawnType(q.Type)
	if !res.OK {
		return nil, errors.New(res.Message)
	}
	resolved := res.Type
	cfg := getAgentConfig(resolved)
	if cfg == nil {
		return nil, fmt.Errorf("Unknown agent type: %q", q.Type)
	}
	if feature := unportedAgentFeature(cfg); feature != "" {
		return nil, refusal(feature)
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
	var extras promptExtras
	if names, ok := skills.([]string); ok {
		extras.SkillBlocks = preloadSkills(names, cwd)
	}
	system := buildAgentPrompt(cfg, cwd, detectEnv(ctx, cwd), parentPrompt, extras)
	plan, scope, err := a.childScope(ctx, resolved, cfg, q.Isolated, getToolNamesForType(resolved), cwd, nil)
	if err != nil {
		return nil, err
	}
	spec := childSpec{Prompt: q.Prompt, SystemPrompt: system, PromptMode: "replace", Cwd: cwd, Thinking: q.Thinking,
		NoExtensions: scope.NoExtensions, ExtensionPaths: scope.ExtensionPaths, AllowTools: scope.AllowTools, ExcludeTools: scope.ExcludeTools,
		NoSkills: skills != true, Warnings: plan.Warnings}
	if trusted, err := ctx.IsProjectTrusted(); err == nil {
		spec.Trust = "no-approve"
		if trusted {
			spec.Trust = "approve"
		}
	}
	if sessionPersisted(cfg, false, remember) {
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
	if cfg.IsDefault && cfg.Name == "Explore" {
		spec.Model = "" // the original's haiku default falls back to the parent's model when it is not available; this port never forces it
	} else {
		spec.Model = cfg.Model
	}
	if q.Model != "" {
		spec.Model = q.Model
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
	case q.MaxTurns > 0:
		spec.MaxTurns = q.MaxTurns
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
	description := q.Description
	if description == "" {
		description = strings.Join(strings.Fields(q.Prompt)[:min(5, len(strings.Fields(q.Prompt)))], " ")
	}
	return a.mgr.spawnWith(resolved, description, q.Name, spec, spawnOptions{Background: q.Background, BypassQueue: q.BypassQueue, ResumeFile: q.ResumeFile, Reclaim: q.Reclaim}), nil
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
