package pi_subagents

import (
	"strings"
	"sync"
	"time"
)

// The permission arbiter (watchdog/permission-arbiter.ts): a model decides whether one child tool call may run. It fails closed:
// whatever goes wrong, the answer is a denial with the reason.

type watchdogPermissionResult struct {
	Approved bool
	Reason   string
	Source   string
}

type watchdogPermissionRequest struct {
	Ctx               wdContext
	ToolName          string
	Args              any
	RawWatchdogConfig string
	AuditPath         string
	Signal            <-chan struct{}
}

type watchdogPermissionArbiter func(req watchdogPermissionRequest) watchdogPermissionResult

var wdDecisionSchema = wdToolSchema(map[string]any{
	"decision": map[string]any{"type": "string", "enum": []any{"approve", "deny"}},
	"reason":   map[string]any{"type": "string", "description": "One concise reason for this exact decision."},
}, []string{"decision", "reason"})

func wdConciseReason(value string) string {
	trimmed := jsTrim(value)
	if trimmed == "" {
		return "Watchdog returned an empty reason."
	}
	return wdSlice(trimmed, 0, 500)
}

func wdNowMillis() float64 { return float64(time.Now().UnixMilli()) }

// createWatchdogPermissionArbiter: streamFn nil asks the session model registry.
func createWatchdogPermissionArbiter(streamFn wdStreamFn) watchdogPermissionArbiter {
	return func(req watchdogPermissionRequest) watchdogPermissionResult {
		preview := permissionArgsPreview(req.Args)
		createdAt := wdNowMillis()
		_ = appendPermissionAudit(req.AuditPath, wdMsg("type", "permission.request", "createdAt", createdAt, "toolName", req.ToolName, "preview", preview, "matchedRule", "ask", "decisionSource", "watchdog"))
		var mu sync.Mutex
		completed := false
		finish := func(approved bool, reason, decision string) watchdogPermissionResult {
			concise := wdConciseReason(reason)
			mu.Lock()
			defer mu.Unlock()
			if completed {
				return watchdogPermissionResult{Approved: approved, Reason: concise, Source: "watchdog"}
			}
			completed = true
			_ = appendPermissionAudit(req.AuditPath, wdMsg("type", "permission.decision", "createdAt", wdNowMillis(), "requestCreatedAt", createdAt, "toolName", req.ToolName,
				"decision", decision, "approved", approved, "decisionSource", "watchdog", "reason", concise))
			return watchdogPermissionResult{Approved: approved, Reason: concise, Source: "watchdog"}
		}

		childConfig, err := decodeChildWatchdogConfig(req.RawWatchdogConfig)
		if err != nil {
			return finish(false, "Watchdog permission arbiter configuration is invalid: "+err.Error(), "unavailable")
		}
		if childConfig == nil {
			return finish(false, "Watchdog permission arbiter is unavailable because the child watchdog is disabled.", "unavailable")
		}
		if wdClosed(req.Signal) || wdClosed(req.Ctx.Signal()) {
			return finish(false, "Watchdog permission decision was cancelled.", "cancelled")
		}

		var decision *jsObject
		var agent *wdAgent
		var agentMu sync.Mutex
		abortAgent := func() {
			agentMu.Lock()
			defer agentMu.Unlock()
			if agent != nil {
				agent.abort()
			}
		}
		tool := wdAgentTool{
			Spec: wdToolSpec{Name: "watchdog_permission_decision", Description: "Approve or deny this exact child tool call. Call exactly once.", Parameters: wdDecisionSchema},
			Execute: func(args *jsObject) (string, error) {
				mu.Lock()
				if decision == nil {
					decision = args
				}
				mu.Unlock()
				return "Permission decision recorded.", nil
			},
		}
		type outcome struct {
			result watchdogPermissionResult
			err    error
		}
		done := make(chan outcome, 1)
		go func() {
			defer func() {
				if r := recover(); r != nil {
					done <- outcome{err: wdPanicError(r)}
				}
			}()
			config := childResolvedConfig(*childConfig)
			selection, err := resolveWatchdogReviewModel(req.Ctx, config, "")
			if err != nil {
				done <- outcome{err: err}
				return
			}
			section, err := formatWatchdogCwdSection(req.Ctx.Cwd())
			if err != nil {
				done <- outcome{err: err}
				return
			}
			system := strings.Join([]string{
				"You are the pi-subagents watchdog permission arbiter.",
				"Decide only whether this exact non-bash child tool call should proceed.",
				"Call watchdog_permission_decision exactly once with approve or deny and a concise reason.",
				"Deny when uncertain. Do not produce freeform advice or ask the parent orchestrator.",
				"",
				section,
			}, "\n")
			stream := wdStreamWithAuth(req.Ctx, streamFn, selection.Auth, nil)
			a := newWdAgent(system, selection.Model, selection.ThinkingLevel, []wdAgentTool{tool}, stream)
			a.apiKey = selection.Auth.APIKey
			a.beforeToolCall = func(name string) (string, bool) {
				if name == tool.Spec.Name {
					return "", false
				}
				return "Permission arbiter tool '" + name + "' is not allowed.", true
			}
			agentMu.Lock()
			agent = a
			agentMu.Unlock()
			a.prompt("Tool: " + req.ToolName + "\nRedacted arguments: " + preview)
			mu.Lock()
			d := decision
			mu.Unlock()
			if d == nil {
				done <- outcome{result: finish(false, "Watchdog permission arbiter returned no decision.", "malformed")}
				return
			}
			kind := wdPropStr(d, "decision")
			done <- outcome{result: finish(kind == "approve", wdPropStr(d, "reason"), kind)}
		}()

		timer := time.NewTimer(time.Duration(childConfig.AgentEndTimeoutMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case o := <-done:
			if o.err != nil {
				reason := o.err.Error()
				kind := "error"
				if strings.Contains(reason, "timed out") {
					kind = "timeout"
				}
				return finish(false, "Watchdog permission arbiter failed closed: "+reason, kind)
			}
			return o.result
		case <-timer.C:
			abortAgent()
			return finish(false, "Watchdog permission decision timed out.", "timeout")
		case <-req.Signal:
			abortAgent()
			return finish(false, "Watchdog permission decision was cancelled.", "cancelled")
		case <-req.Ctx.Signal():
			abortAgent()
			return finish(false, "Watchdog permission decision was cancelled.", "cancelled")
		}
	}
}

// requestWatchdogPermission is the arbiter on the session's model registry.
var requestWatchdogPermission = createWatchdogPermissionArbiter(nil)
