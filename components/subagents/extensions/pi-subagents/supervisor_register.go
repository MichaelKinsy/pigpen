package pi_subagents

import (
	"errors"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The parent's side of the supervisor channel in a session: the channel is created at session start (the session's runtime id
// becomes the owner of the requests it accepts), polls only while child work is live, and answers the `subagent_supervisor` tool.

var errNoSession = errors.New("no session to deliver to")

var supervisorRT = struct {
	mu      sync.Mutex
	channel *nativeSupervisorChannel
	offReg  func()
}{}

func currentSupervisorChannel() *nativeSupervisorChannel {
	supervisorRT.mu.Lock()
	defer supervisorRT.mu.Unlock()
	return supervisorRT.channel
}

// supervisorFindPendingAsks is the pending-ask lookup of a steer: a fresh, read-only look at the child's mailbox.
func supervisorFindPendingAsks(_ *SubagentState) func(runID, agent string, childIndex int) []string {
	return func(runID, agent string, childIndex int) []string {
		ch := currentSupervisorChannel()
		if ch == nil {
			return nil
		}
		ids, err := ch.findPendingAsks(runID, agent, childIndex)
		if err != nil {
			return nil
		}
		return ids
	}
}

func sessionSupervisorHost() supervisorHost {
	ctxOf := func() (sdk.Context, bool) { return bgRT.context() }
	return supervisorHost{
		// The tool is declared with the extension, so the channel never registers it.
		GetAllTools: func() []string { return []string{nativeSupervisorToolName} },
		SendMessage: func(msg sdk.CustomMessage, trigger bool) error {
			ctx, ok := ctxOf()
			if !ok {
				return errNoSession
			}
			return ctx.SendCustomMessage(msg, sdk.SendMessageOptions{TriggerTurn: &trigger})
		},
		AppendEntry: func(customType string, data any) error {
			ctx, ok := ctxOf()
			if !ok {
				return errNoSession
			}
			return ctx.AppendEntry(customType, data)
		},
		Emit: func(channel string, payload any) {
			if ctx, ok := ctxOf(); ok {
				_ = ctx.Events().Emit(channel, payload)
			}
		},
	}
}

func startSupervisorChannel(ctx sdk.Context) {
	rt := bgRT
	id, _ := ctx.GetSessionID()
	rt.mu.Lock()
	rt.state.SupervisorOwnerSessionID = id
	rt.mu.Unlock()
	supervisorRT.mu.Lock()
	old := supervisorRT.channel
	supervisorRT.channel = createNativeSupervisorChannel(sessionSupervisorHost(), rt.state, supervisorDeps{StateLock: &rt.mu})
	ch := supervisorRT.channel
	if supervisorRT.offReg != nil {
		supervisorRT.offReg()
	}
	// Work going live (a child registering) is the demand that starts polling where there is no native watcher.
	supervisorRT.offReg = onChildRegistered(func(registeredChild) { ch.activateTransport() })
	supervisorRT.mu.Unlock()
	if old != nil {
		old.dispose()
	}
	ch.start()
}

func stopSupervisorChannel() {
	rt := bgRT
	rt.mu.Lock()
	// the runtime owner is cleared with the session, whatever a stale UI context still says
	rt.state.SupervisorOwnerSessionID = ""
	rt.mu.Unlock()
	supervisorRT.mu.Lock()
	ch := supervisorRT.channel
	supervisorRT.channel = nil
	if supervisorRT.offReg != nil {
		supervisorRT.offReg()
		supervisorRT.offReg = nil
	}
	supervisorRT.mu.Unlock()
	if ch != nil {
		ch.dispose()
	}
}

func init() {
	extensionHooks = append(extensionHooks, func(e *sdk.Extension) {
		defs := toolDefs()
		e.RegisterTool(sdk.ToolDefinition{Name: nativeSupervisorToolName, Label: "Subagent Supervisor", Description: defs[nativeSupervisorToolName].Description,
			Parameters: defs[nativeSupervisorToolName].Parameters,
			Execute: func(ctx sdk.Context, p map[string]any) (any, error) {
				ch := currentSupervisorChannel()
				if ch == nil {
					startSupervisorChannel(ctx)
					ch = currentSupervisorChannel()
				}
				res, err := ch.execute(supervisorParamsOf(p))
				if err != nil {
					return nil, sdk.NewToolError(err.Error())
				}
				return sdk.ToolResult{Content: res.Text, Details: res.Details}, nil
			}})
		e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
			startSupervisorChannel(ctx)
			return nil, nil
		})
		e.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
			stopSupervisorChannel()
			return nil, nil
		})
	})
}
