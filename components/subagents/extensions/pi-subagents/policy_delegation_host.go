package pi_subagents

import (
	"context"
	"errors"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The delegation bridge on the host: it listens on the extension's event bus (the SDK's `pi.events`, shared with every other
// extension of the session) and runs one delegated leaf per accepted request on a child pig process.

func init() { extensionHooks = append(extensionHooks, installDelegationBridge) }

// delegationDispatch is the context of the dispatch in progress; the bridge reads it through GetContext.
type delegationDispatch struct {
	mu  sync.Mutex
	ctx sdk.Context
	set bool
}

// sdkDelegationEvents adapts the SDK's event bus to the bridge. A listener runs with the context of the request the host
// dispatched.
type sdkDelegationEvents struct {
	bus      sdk.EventBus
	dispatch *delegationDispatch
}

func (s sdkDelegationEvents) On(event string, handler func(any)) func() {
	unsubscribe, err := s.bus.On(event, func(ctx sdk.Context, data any) error {
		s.dispatch.mu.Lock()
		s.dispatch.ctx, s.dispatch.set = ctx, true
		s.dispatch.mu.Unlock()
		handler(data)
		return nil
	})
	if err != nil {
		return nil
	}
	return unsubscribe
}

func (s sdkDelegationEvents) Emit(event string, data any) { _ = s.bus.Emit(event, data) }

// delegationHostContext is what a delegated leaf needs of the dispatching context once the dispatch is over: the context
// itself does not outlive it, so the session identity is read while it does.
type delegationHostContext struct{ SessionID string }

func installDelegationBridge(e *sdk.Extension) {
	if childRole() != nil {
		return
	}
	dispatch := &delegationDispatch{}
	registerPromptTemplateDelegationBridge(delegationBridgeOptions{
		Events: sdkDelegationEvents{bus: e.Events(), dispatch: dispatch},
		GetContext: func() (any, bool) {
			dispatch.mu.Lock()
			ctx, set := dispatch.ctx, dispatch.set
			dispatch.mu.Unlock()
			if !set {
				return nil, false
			}
			return delegationHostContext{SessionID: sessionIDOf(ctx)}, true
		},
		Execute:           executeDelegatedLeaf,
		ExecuteStructured: executeDelegatedLeaf,
	})
}

func delegationFailure(agent, message string) (*bridgeResult, error) {
	return &bridgeResult{IsError: true, Content: []any{map[string]any{"type": "text", "text": message}},
		Details: &bridgeDetails{Mode: "single", Results: []bridgeChild{{Agent: agent, ExitCode: new(1.0), Error: message}}}}, nil
}

// executeDelegatedLeaf runs one delegated leaf (a single foreground child, no clarify UI). What the port does not run is
// refused with the port's one message, never ignored.
func executeDelegatedLeaf(_ string, params map[string]any, ctx context.Context, bridgeCtx any, _ func(*bridgeResult)) (*bridgeResult, error) {
	host, _ := bridgeCtx.(delegationHostContext)
	name, _ := params["agent"].(string)
	for _, p := range []struct{ key, feature string }{
		{"toolBudget", "the `toolBudget` option of a delegated run"}, {"skill", "the `skill` option of a delegated run"},
		{"intercomBridge", "the `intercomBridge` option of a delegated run"}, {"artifacts", "the `artifacts` option of a delegated run"},
	} {
		if params[p.key] != nil {
			return delegationFailure(name, refusal(p.feature))
		}
	}
	if params["outputSchema"] != nil {
		return delegationFailure(name, refusal("a structured result of a delegated run (`result.kind: structured`)"))
	}
	cwd, _ := params["cwd"].(string)
	d := DiscoverAgents(cwd, ScopeBoth)
	a, msg := ResolveAgentName(name, d.Agents)
	if a == nil {
		if diag := blockingDiagnostic(name, nil, d.Diagnostics); diag != nil {
			msg = "Agent '" + name + "' has invalid configuration: " + diag.Error
		} else if msg == "" {
			msg = formatUnknownAgentError(name, Discovery{Agents: d.Agents, Cwd: d.Cwd, Directories: d.Directories})
		}
		return delegationFailure(name, msg)
	}
	if m := capabilityCeilingAgentRestrictionMessage(a.Name, resolveSubagentCapabilityCeiling(host.SessionID, nil)); m != "" {
		return delegationFailure(a.Name, m)
	}
	agent := *a
	thinking, _ := params["delegatedThinkingOverride"].(string)
	if thinking != "" {
		agent.Thinking = thinking
	}
	model, _ := params["model"].(string)
	timeoutMs := 0
	if t, ok := asNumber(params["timeoutMs"]); ok {
		timeoutMs = int(t)
	}
	task, _ := params["task"].(string)
	done := ctx.Done()
	out, err := runAgentOpts(done, cwd, &agent, task, model, runOptions{TimeoutMs: timeoutMs})
	child := bridgeChild{Agent: agent.Name, Model: model, Thinking: thinking}
	var te *timeoutError
	switch {
	case errors.As(err, &te):
		child.TimedOut, child.Error, child.ExitCode = true, err.Error(), new(1.0)
		child.FinalOutput = &te.Stdout
		return &bridgeResult{IsError: true, Details: &bridgeDetails{Mode: "single", TimedOut: true, Results: []bridgeChild{child}}}, nil
	case err != nil:
		if ctx.Err() != nil {
			return nil, err
		}
		return delegationFailure(agent.Name, err.Error())
	}
	child.ExitCode, child.FinalOutput = new(0.0), &out
	return &bridgeResult{Details: &bridgeDetails{Mode: "single", Results: []bridgeChild{child}}}, nil
}
