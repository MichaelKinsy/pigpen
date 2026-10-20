package pi_subagents

import (
	"regexp"

	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The main-session watchdog registration (watchdog/register-main.ts): the `/subagents-watchdog` command, the message and entry
// renderers of its warnings, and the session events that feed the runtime.

func wdBoolLabel(v bool) string { panic("not implemented") }

func wdSourceLine(s wdSettingsSource) string { panic("not implemented") }

func wdCurrentSessionModelLine(ctx wdContext) string { panic("not implemented") }

func wdMainThinkingLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	panic("not implemented")
}

func wdMainModelLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	panic("not implemented")
}

func wdThinkingText(t wdThinking, unset string) string { panic("not implemented") }

func wdChildrenLine(snapshot watchdogRuntimeSnapshot) string { panic("not implemented") }

func wdRecommendationLine(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	panic("not implemented")
}

func wdLspLine(snapshot watchdogRuntimeSnapshot) string { panic("not implemented") }

func buildWatchdogStatus(snapshot watchdogRuntimeSnapshot, ctx wdContext) string {
	panic("not implemented")
}

var wdTestCommandRe = regexp.MustCompile(`^test\s+(concern|blocker)\s+(low|medium|high)\s+([\s\S]+)$`)

func wdFormatThinking(t wdThinking) string { panic("not implemented") }

type wdModelCommandValue struct {
	model       wdPatch[string]
	thinking    wdPatch[wdThinking]
	description string
}

func wdResolveModelCommandValue(rt *watchdogRuntime, ctx wdContext, raw string) (wdModelCommandValue, error) {
	panic("not implemented")
}

func wdBuildCommandRecommendation(rt *watchdogRuntime, ctx wdContext) (string, error) {
	panic("not implemented")
}

func wdBuildCommandCheck(rt *watchdogRuntime, ctx wdContext) (string, error) {
	panic("not implemented")
}

func wdTestWarning(severity, importance, text string) wdWarning { panic("not implemented") }

// watchdogCommandHost is what the command writes through: a text message and a notification.
type watchdogCommandHost struct {
	text   func(string)
	notify func(message, level string)
}

// handleWatchdogCommand runs `/subagents-watchdog <args>`.
func handleWatchdogCommand(host watchdogCommandHost, rt *watchdogRuntime, args string, ctx wdContext) {
	panic("not implemented")
}

// ── registration ──

// watchdogHost holds the state the registered handlers share: the runtime and the context of the latest event.
type watchdogHost struct {
	mu       sync.Mutex
	ctx      sdk.Context
	has      bool
	baseline *watchdogDiffBaseline
	rt       *watchdogRuntime
}

func (h *watchdogHost) remember(ctx sdk.Context) { panic("not implemented") }

func (h *watchdogHost) current() (sdk.Context, bool) { panic("not implemented") }

func (h *watchdogHost) currentWd() wdContext { panic("not implemented") }

func (h *watchdogHost) diffBaseline() *watchdogDiffBaseline { panic("not implemented") }

func wdRunContext(ctx sdk.Context) wdRunCtx { panic("not implemented") }

func wdSendWarning(ctx sdk.Context, details wdWarning, opts *wdSendOptions) { panic("not implemented") }

// watchdogRuntimeFor builds the main watchdog runtime wired to the host.
func newMainWatchdogHost() *watchdogHost { panic("not implemented") }

func (h *watchdogHost) runtimeOptions() watchdogRuntimeOptions { panic("not implemented") }

func wdRenderWarning(message map[string]any, expanded bool) []string { panic("not implemented") }

func wdMessageText(content any) string { panic("not implemented") }

func wdWarningFromMap(m map[string]any) wdWarning { panic("not implemented") }

// dispatch is the body of the handler registered for a session event (register-main.ts's pi.on calls).
func (h *watchdogHost) dispatch(name string, event any, rc wdRunCtx) { panic("not implemented") }

// wdMainEvents are the events the main watchdog listens to.
var wdMainEvents = []string{sdk.EventSessionStart, sdk.EventBeforeAgentStart, sdk.EventTurnEnd, sdk.EventInput, sdk.EventModelSelect, sdk.EventToolResult,
	sdk.EventAgentEnd, sdk.EventSessionBeforeSwitch, sdk.EventSessionBeforeFork, sdk.EventSessionCompact, sdk.EventSessionShutdown}

// wdRemembers are the events whose context the watchdog keeps for the reviews and messages it sends later.
var wdRemembers = map[string]bool{sdk.EventSessionStart: true, sdk.EventBeforeAgentStart: true, sdk.EventTurnEnd: true, sdk.EventToolResult: true, sdk.EventAgentEnd: true}

// wdClarificationMessage is how a question of the reviewer reaches the orchestrator: a visible steer that starts a turn.
func wdClarificationMessage(content string) (sdk.CustomMessage, sdk.SendMessageOptions) {
	panic("not implemented")
}

func registerMainWatchdog(e *sdk.Extension) *watchdogHost { panic("not implemented") }

// watchdogTool is the runtime the `subagent` tool's watchdog actions talk to.
var watchdogMain *watchdogHost

func init() { panic("not implemented") }

func runWatchdogToolAction(name string) func(ctx sdk.Context, params map[string]any) (any, error) {
	panic("not implemented")
}

// wdCwdOverride is a context whose cwd is the tool call's resolved cwd parameter.
type wdCwdOverride struct {
	wdContext
	cwd string
}

func (c wdCwdOverride) Cwd() string { panic("not implemented") }
