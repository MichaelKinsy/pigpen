// Package pi_permission_system is a Go port (partial) of @gotgenes/pi-permission-system: allow, ask and deny rules for tool
// calls, read from the global config. It ports the rule engine, the ask dialog (with session approvals), the tool_call gate for
// the tools and paths it can decide, and the skill gates; see port/PORT.md for what is not ported.
package pi_permission_system

import (
	"strings"
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
	// sessionRules are the session approvals the dialog's "for this session" option recorded (the original's
	// SessionRules store): allow rules appended after the config's at check time, cleared with the session.
	sessionRules Ruleset
	// agentName is the last known active agent (session.resolveAgentName's knownAgentName).
	agentName string
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

// decide gates one tool call against the current policy, the session approvals and the skill-read gate, and
// refuses (fail-closed) a call a scope the port cannot read could change.
func (s *state) decide(ctx sdk.Context, name string, input map[string]any) Verdict {
	p := s.current()
	s.mu.Lock()
	session := append(Ruleset(nil), s.sessionRules...)
	skills, cwd := s.skills, s.cwd
	s.mu.Unlock()
	v := p.DecideIn(name, input, session, ctx.Cwd())
	if !v.Block && v.Ask == nil {
		s.mu.Lock()
		v = skillRead(name, input, skills, cwd)
		s.mu.Unlock()
	}
	if !v.Block {
		if feature := s.unportedScope(ctx); feature != "" {
			v = unsupported(feature)
		}
	}
	return v
}

// recordSession stores an ask's session grant, so the next matching call is allowed without asking.
func (s *state) recordSession(a Ask) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionRules = append(s.sessionRules, a.sessionGrants()...)
}

// blockResult is the tool_call handler's block reply.
func blockResult(reason string) map[string]any {
	return map[string]any{"block": true, "reason": reason}
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
		st.sessionRules = nil
		st.agentName = ""
		st.mu.Unlock()
		syncStatus(ctx, p)
		if st.config.Issue != "" {
			ctx.Notify(st.config.Issue, "warning")
		}
		if len(st.config.Unported) > 0 {
			ctx.Notify(refusal("the config option(s) '"+strings.Join(st.config.Unported, "', '")+"' (they are accepted and ignored)"), "warning")
		}
		if feature := st.unportedScope(ctx); feature != "" {
			ctx.Notify(refusal(feature)+" Until then every call the port would allow is blocked.", "warning")
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
		// PiG 0.5 hands the handler a *sdk.SystemPrompt in data["systemPrompt"], not a string; GetSystemPrompt reads its rendered text.
		prompt, _ := ctx.GetSystemPrompt()
		agent := ""
		if entries, err := ctx.GetEntries(); err == nil {
			agent = agentFromEntries(entries)
		}
		if agent == "" {
			agent = agentFromSystemPrompt(prompt)
		}
		if agent != "" {
			st.mu.Lock()
			st.agentName = agent
			st.mu.Unlock()
		}
		skills := p.visibleSkillEntries(prompt, ctx.Cwd())
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
		st.mu.Lock()
		st.sessionRules = nil
		st.mu.Unlock()
		ctx.SetStatus(extensionID, "")
		return nil, nil
	})
	e.OnEvent(sdk.EventToolCall, func(ctx sdk.Context, data map[string]any) (any, error) {
		name, _ := data["toolName"].(string)
		input, _ := data["input"].(map[string]any)
		v := st.decide(ctx, name, input)
		if v.Ask != nil {
			// The skill gates carry no session approval in the original, so their dialog offers none.
			d := promptAsk(ctx, *v.Ask, v.Ask.Surface != "skill")
			if !d.approved {
				if d.unavailable {
					return blockResult(askUnavailable(*v.Ask)), nil
				}
				return blockResult(askDenial(*v.Ask, d.reason)), nil
			}
			if d.forSession {
				st.recordSession(*v.Ask)
			}
		}
		if v.Block {
			return blockResult(v.Reason), nil
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventInput, st.skillInput)
	return e
}
