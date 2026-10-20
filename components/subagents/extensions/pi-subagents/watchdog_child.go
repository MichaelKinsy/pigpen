package pi_subagents

import (
	"sync"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The child-side watchdog (watchdog/register-child.ts): the same runtime as the main session's, in the child, reporting status
// events to the run that started it. The original hands the child's host a status callback; a pig child is a separate process, so
// the events travel as JSON lines in a file the role names (WatchdogStatusFile) and the parent tails it (tailChildWatchdogStatus).

// childResolvedConfig is the config a child's runtime resolves to: the watchdog on, its model and bounds from the child config.
func childResolvedConfig(c childWatchdogConfig) *watchdogConfig { panic("not implemented") }

// childWatchdogBinding is what the child watchdog sends through: warning messages and entries of the child's session.
type childWatchdogBinding struct {
	SendMessage   func(msg watchdogWarningMessage, opts *wdSendOptions)
	AppendEntry   func(customType string, details wdWarning)
	ThinkingLevel func() string
}

type childWatchdogOptions struct {
	// Review, RepoChangeSignature and LspDiagnostics replace the real ones (tests); nil keeps them.
	Review              watchdogReviewFunc
	RepoChangeSignature func(cwd string) *watchdogRepoChangeSignature
	LspDiagnostics      watchdogLspFunc
	ReviewDescription   string
}

type childWatchdog struct {
	Runtime *watchdogRuntime

	config       childWatchdogConfig
	writeStatus  func(childWatchdogStatusEvent)
	mu           sync.Mutex
	seq          int
	agentSettled bool
}

func (c *childWatchdog) emitStatus(phase, reason string, warning *childWatchdogWarningSummary) {
	panic("not implemented")
}

func wdWarningSummary(d wdWarning) *childWatchdogWarningSummary { panic("not implemented") }

func (c *childWatchdog) childDetails(d wdWarning) wdWarning { panic("not implemented") }

// newChildWatchdog builds the child's runtime. cfg must be enabled; writeStatus is the status sink.
func newChildWatchdog(cfg childWatchdogConfig, writeStatus func(childWatchdogStatusEvent), binding childWatchdogBinding, review watchdogReviewFunc, opts childWatchdogOptions) (*childWatchdog, error) {
	panic("not implemented")
}

func (c *childWatchdog) onSessionStart(ctx wdRunCtx) { panic("not implemented") }

func (c *childWatchdog) onBeforeAgentStart(event any, ctx wdRunCtx) { panic("not implemented") }

func (c *childWatchdog) onTurnEnd(event any, ctx wdRunCtx, structuredTerminal bool) {
	panic("not implemented")
}

func (c *childWatchdog) onToolResult(ctx wdRunCtx) { panic("not implemented") }

func (c *childWatchdog) onAgentEnd(event any, ctx wdRunCtx) { panic("not implemented") }

func (c *childWatchdog) onAgentSettled() { panic("not implemented") }

func (c *childWatchdog) onSessionShutdown() { panic("not implemented") }

// ── the child process ──

func (e childWatchdogStatusEvent) toObject() *jsObject { panic("not implemented") }

// childWatchdogStatusFileWriter appends each status event as one JSON line.
func childWatchdogStatusFileWriter(path string) func(childWatchdogStatusEvent) {
	panic("not implemented")
}

// tailChildWatchdogStatus delivers every status event a child appended to path, once, as the original's sink delivers them; it
// polls the file until stop is called (after one last read).
func tailChildWatchdogStatus(path string, onEvent func(event childWatchdogStatusEvent)) (stop func()) {
	panic("not implemented")
}

func wdStructuredTerminal(cfg *childRoleConfig) func() bool { panic("not implemented") }

// registerChildWatchdogRole is the child role's registration: the watchdog of the config the parent passed, the watchdog_diff
// tool when the child requires it.
func registerChildWatchdogRole(e *sdk.Extension, cfg *childRoleConfig) error {
	panic("not implemented")
}

func init() { panic("not implemented") }
