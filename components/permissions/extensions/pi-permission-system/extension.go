// Package pi_permission_system is a Go port (partial) of @gotgenes/pi-permission-system: allow, ask and deny rules for tool calls,
// read from the global config. It ports the rule engine and the tool_call gate for tools whose decision does not depend on a path;
// see port/PORT.md for what is not ported.
package pi_permission_system

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

type state struct {
	mu       sync.Mutex
	policy   *Policy
	config   Config
	baseline surfaceBaseline
	skills   []skillEntry // the listed skills the policy does not deny, from the last system prompt
	cwd      string
}

func (s *state) reload() *Policy {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = loadConfig()
	s.policy = NewPolicy(s.config)
	return s.policy
}

func (s *state) current() *Policy {
	s.mu.Lock()
	p := s.policy
	s.mu.Unlock()
	if p == nil {
		return s.reload()
	}
	return p
}

func syncStatus(ctx sdk.Context, p *Policy) {
	if p.yolo {
		ctx.SetStatus(extensionID, "yolo")
		return
	}
	ctx.SetStatus(extensionID, "")
}

// Extension returns the permission gate.
func Extension() *sdk.Extension {
	e := sdk.New(extensionID)
	st := &state{}

	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		p := st.reload()
		st.mu.Lock()
		st.baseline.reset()
		st.skills = nil
		st.mu.Unlock()
		syncStatus(ctx, p)
		if st.config.Issue != "" {
			ctx.Notify(st.config.Issue, "warning")
		}
		if msg := detectPermissiveBashFallback(st.config.Permission); msg != "" {
			ctx.Notify(msg, "warning")
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		p := st.reload()
		syncStatus(ctx, p)
		// The skills the prompt lists, for the skill-read gate (before-agent-start.ts setActiveSkillEntries).
		skills := p.visibleSkillEntries(promptText(data["systemPrompt"]), ctx.Cwd())
		st.mu.Lock()
		st.skills, st.cwd = skills, ctx.Cwd()
		st.mu.Unlock()
		// Withhold the tools the policy denies outright, and restore ones it no longer denies.
		active, err := ctx.GetActiveTools()
		if err != nil {
			return nil, nil
		}
		registered := map[string]bool{}
		if all, err := ctx.GetAllTools(); err == nil {
			for _, t := range all {
				registered[t.Name] = true
			}
		}
		st.mu.Lock()
		res := st.baseline.resolveExposed(active, registered, func(n string) bool { return !p.IsToolFullyDenied(n) })
		st.mu.Unlock()
		ctx.SetActiveTools(res.exposed)
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		ctx.SetStatus(extensionID, "")
		return nil, nil
	})
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["toolName"].(string)
		input, _ := data["input"].(map[string]any)
		v := st.current().Decide(name, input)
		if !v.Block {
			st.mu.Lock()
			v = skillRead(name, input, st.skills, st.cwd)
			st.mu.Unlock()
		}
		if v.Block {
			return map[string]any{"block": true, "reason": v.Reason}, nil
		}
		return nil, nil
	})
	return e
}
