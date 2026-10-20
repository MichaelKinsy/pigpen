package pi_subagents

import (
	"fmt"
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The schedule actions of the subagent tool and the manager's life in one extension instance: bound to the session at its start,
// stopped at its end, told when an async run completes.

// scheduleActionParams are the parameters each schedule action reads (the original's schema fields it validates or uses).
var scheduleActionParams = map[string][]string{
	"schedule.create": {"id", "name", "at", "every", "workflowScript", "workflowScriptPath", "args", "baseRef", "cwd", "overlap", "catchUp", "timeoutMs", "sessionOnly", "quiet",
		"context", "async", "agent", "task", "tasks", "chain", "acceptance", "gate", "on", "timezone", "missionId", "mission", "missionUpdate", "missionStatus", "missionScope"},
	"schedule.list":    {"cwd"},
	"schedule.show":    {"id", "cwd"},
	"schedule.history": {"id", "cwd"},
	"schedule.pause":   {"id", "cwd"},
	"schedule.resume":  {"id", "cwd"},
	"schedule.run":     {"id", "cwd", "quiet"},
	"schedule.run-due": {"cwd"},
	"schedule.delete":  {"id", "cwd"},
}

var (
	schedRTMu sync.Mutex
	schedMgr  *scheduledRunManager
)

func init() {
	for _, name := range scheduledRunActions {
		n := name
		registerAction(n, scheduleActionParams[n], func(ctx sdk.Context, p map[string]any) (any, error) { return runScheduleAction(ctx, n, p) })
	}
	extensionHooks = append(extensionHooks, installScheduleRuntime)
}

// scheduleConfigFromFile reads config.scheduledRuns (enabled, maxPending, storeRoot); an invalid config reads as empty.
func scheduleConfigFromFile() scheduleConfig {
	cfg := loadStrictExtensionConfig()
	sr, ok := asObject(cfg.vals["scheduledRuns"])
	if !ok {
		return scheduleConfig{}
	}
	var out scheduleConfig
	if b, ok := sr.vals["enabled"].(bool); ok {
		out.Enabled = &b
	}
	if f, ok := sr.vals["maxPending"].(float64); ok && isIntegerNum(f) && f >= 1 {
		n := int(f)
		out.MaxPending = &n
	}
	if s, ok := sr.vals["storeRoot"].(string); ok && s != "" {
		if root, err := resolveScheduledStoreRoot(s); err == nil {
			out.StoreRoot = root
		}
	}
	return out
}

func scheduleCtxOf(ctx sdk.Context) scheduleCtx {
	c := scheduleCtx{Cwd: ctx.Cwd(), SessionID: sessionIDOf(ctx), Live: ctx}
	if f, err := ctx.GetSessionFile(); err == nil && f != nil {
		c.SessionFile = *f
	}
	return c
}

// scheduledLaunch is executeScheduled: a due schedule starts its async workflow with the context of the session that owns it.
func scheduledLaunch(params map[string]any, c scheduleCtx) scheduleLaunchResult {
	live, ok := c.Live.(sdk.Context)
	if !ok {
		return scheduleLaunchResult{IsError: true, Text: "Schedule runtime context is unavailable."}
	}
	res, err := executeWorkflowRun(live, params)
	o := outcomeOf(res, err)
	out := scheduleLaunchResult{Text: o.Text, IsError: o.IsError}
	if o.Details != nil {
		out.AsyncID = objStr(o.Details, "asyncId")
		if out.AsyncID == "" {
			out.AsyncID = objStr(o.Details, "runId")
		}
		out.AsyncDir = objStr(o.Details, "asyncDir")
	}
	return out
}

func installScheduleRuntime(e *sdk.Extension) {
	cfg := scheduleConfigFromFile()
	mgr := newScheduledRunManager(scheduledRunDeps{Config: cfg, StoreRoot: cfg.StoreRoot, Launch: scheduledLaunch,
		CapabilityCeilingActive: func(sessionID string) bool { return resolveSubagentCapabilityCeiling(sessionID, nil) != nil }})
	schedRTMu.Lock()
	schedMgr = mgr
	schedRTMu.Unlock()
	e.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		if err := mgr.bindSession(scheduleCtxOf(ctx)); err != nil {
			scheduleWarn(fmt.Sprintf("[pi-subagents] Scheduled runs could not be restored: %s", err.Error()))
		}
		if bgRT != nil {
			bgRT.bus.On(subagentAsyncCompleteEvent, func(v any) {
				if m, ok := v.(map[string]any); ok {
					mgr.handleAsyncCompletion(m)
				}
			})
		}
		return nil, nil
	})
	e.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
		mgr.stop()
		return nil, nil
	})
}

func runScheduleAction(ctx sdk.Context, name string, p map[string]any) (any, error) {
	schedRTMu.Lock()
	mgr := schedMgr
	schedRTMu.Unlock()
	if mgr == nil {
		return nil, sdk.NewToolError("Schedule runtime is unavailable.")
	}
	params := make(map[string]any, len(p)+1)
	for k, v := range p {
		params[k] = v
	}
	params["action"] = name
	if name == "schedule.create" && defined(params, "workflowScriptPath") {
		script, errText := loadWorkflowScriptPath(ctx.Cwd(), params)
		if errText != "" {
			return nil, sdk.NewToolError(errText)
		}
		delete(params, "workflowScriptPath")
		params["workflowScript"] = script
	}
	res := mgr.handleToolCall(params, scheduleCtxOf(ctx))
	if res.IsError {
		return nil, sdk.NewToolError(res.Text)
	}
	return explicitOK{Content: res.Text, Details: res.Details}, nil
}
