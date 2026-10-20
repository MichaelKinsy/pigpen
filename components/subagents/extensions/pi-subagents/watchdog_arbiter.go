package pi_subagents


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

func wdConciseReason(value string) string { panic("not implemented") }

func wdNowMillis() float64 { panic("not implemented") }

// createWatchdogPermissionArbiter: streamFn nil asks the session model registry.
func createWatchdogPermissionArbiter(streamFn wdStreamFn) watchdogPermissionArbiter {
	panic("not implemented")
}

// requestWatchdogPermission is the arbiter on the session's model registry.
var requestWatchdogPermission = createWatchdogPermissionArbiter(nil)
