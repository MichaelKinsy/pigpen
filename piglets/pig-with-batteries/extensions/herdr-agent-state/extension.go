// Package herdragentstate reports this PiG pane's agent state to herdr, the
// terminal multiplexer that draws agent indicators next to a pane's title.
//
// It is a port of herdr's own Pi integration - the
// `~/.pi/agent/extensions/herdr-agent-state.ts` herdr installs into every Pi
// session - to PiG's Go extension SDK. It keeps the same environment variables,
// socket, methods and parameters, but it reports under PiG's own identity,
// because herdr attributes a pane's agent state to a (source, agent) pair and
// reserves the values herdr's own integrations use. See the README.
//
// The extension deliberately uses only the SDK surface that the PiG validator
// commit pinned by this repository provides, so it builds and registers with the
// same PiG the CI checks run. Where that surface is smaller than the protocol
// supports, the gap is documented in the README rather than worked around.
//
// Outside a herdr pane the extension does nothing, as herdr's integration
// guidance asks. It registers no tools, commands or renderers: its whole
// capability is four event handlers.
package herdragentstate

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// extensionName must match the identity PiG resolves for this directory.
const extensionName = "herdr-agent-state"

// Host event names. The pinned SDK exposes helpers for the session lifecycle but
// not for the agent loop, so these two are the host's event names spelled out.
const (
	eventAgentStart   = "agent_start"
	eventAgentSettled = "agent_settled"
)

// modeTUI is the only run mode with a pane herdr can display. RPC, JSON and
// print modes are headless, and RPC still reports hasUI=true, so the run mode is
// the reliable gate.
const modeTUI = "tui"

// drainTimeout bounds how long session_shutdown waits for queued herdr reports.
const drainTimeout = 2 * time.Second

// agentState is the state herdr renders. The values are herdr's.
type agentState string

const (
	stateIdle    agentState = "idle"
	stateWorking agentState = "working"
)

// Extension builds the extension.
func Extension() *sdk.Extension {
	ext := sdk.New(extensionName)

	socketPath, paneID, ok := herdrPane()
	if !ok {
		// Outside a herdr pane there is nothing to report to and nothing to say
		// about it: register the identity and stay inert.
		return ext
	}
	p := &pane{rep: newReporter(newClient(socketPath), paneID)}

	ext.OnSessionStart(p.onSessionStart)
	ext.OnEvent(eventAgentStart, p.onAgentStart)
	ext.OnEvent(eventAgentSettled, p.onAgentSettled)
	ext.OnSessionShutdown(p.onSessionShutdown)
	return ext
}

// pane is the state machine behind one herdr pane. PiG dispatches every inbound
// handler on its own goroutine, so the fields a handler reads or writes are
// guarded, and host calls are made outside that guard.
type pane struct {
	rep *reporter

	mu sync.Mutex
	// rootSession is set once this session claimed the pane. Every report is
	// suppressed until then, because herdr only tracks panes whose session has
	// announced itself.
	rootSession bool
	// agentActive is true from agent_start until the agent settles idle.
	agentActive bool
	// lastState suppresses a report that would repeat the state herdr already
	// shows.
	lastState agentState
	hasLast   bool
}

// onSessionStart claims the pane and publishes the first state.
func (p *pane) onSessionStart(ctx sdk.Context, data map[string]any) (any, error) {
	if ctx.Mode() != modeTUI {
		return nil, nil
	}

	// The host answers are made before the guard is taken: they cross a socket
	// and must not hold up the blocked listener.
	p.updateSessionRef(ctx)

	p.mu.Lock()
	p.rootSession = true
	// A reload can replace this extension mid-run without another agent_start,
	// so the agent may already be working when this session starts. IsIdle
	// answers idle when the host cannot say, which is the safe direction.
	p.agentActive = !ctx.IsIdle()
	p.mu.Unlock()

	// The session report is enqueued first so herdr attaches the pane to this
	// transcript before the state that belongs to it.
	p.rep.enqueue(p.rep.sessionReport(sdk.Param[string](data, "reason")))
	p.publish(true)
	return nil, nil
}

// onAgentStart marks the agent busy and refreshes the session reference, which
// can change when the session was forked, switched or reloaded.
func (p *pane) onAgentStart(ctx sdk.Context, _ map[string]any) (any, error) {
	p.mu.Lock()
	claimed := p.rootSession
	p.mu.Unlock()
	if !claimed {
		return nil, nil
	}
	p.updateSessionRef(ctx)

	p.mu.Lock()
	p.agentActive = true
	p.mu.Unlock()

	p.rep.enqueue(p.rep.sessionReport(""))
	p.publish(false)
	return nil, nil
}

// onAgentSettled marks the agent idle, but only once the host agrees the
// session is idle: agent_settled also fires for a turn the user interrupted,
// where queued work may still be pending.
func (p *pane) onAgentSettled(ctx sdk.Context, _ map[string]any) (any, error) {
	idle := ctx.IsIdle()

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.rootSession || !idle {
		return nil, nil
	}
	p.agentActive = false
	p.publishLocked()
	return nil, nil
}

// onSessionShutdown drains the queued reports, so the last state reaches herdr
// before this extension's process goes away.
func (p *pane) onSessionShutdown(sdk.Context, map[string]any) (any, error) {
	p.rep.close(drainTimeout)
	return nil, nil
}

// desired is the state herdr should show: work in progress, or ready.
func (p *pane) desired() agentState {
	if p.agentActive {
		return stateWorking
	}
	return stateIdle
}

// publish sends the desired state unless it repeats the last one. A forced
// publish ignores the repeat, which is how session_start claims a pane that
// already has an indicator.
func (p *pane) publish(force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.publishLocked(force)
}

// publishLocked is publish for callers that already hold the guard.
func (p *pane) publishLocked(force ...bool) {
	state := p.desired()
	forced := len(force) > 0 && force[0]
	if !forced && p.hasLast && state == p.lastState {
		return
	}
	p.lastState, p.hasLast = state, true
	p.rep.enqueue(p.rep.stateReport(state))
}

// updateSessionRef re-reads the identity herdr attaches a pane to. Pi reads it
// from its session manager; PiG serves it as a host call, and an absent answer
// clears that field exactly as Pi's missing value does.
func (p *pane) updateSessionRef(ctx sdk.Context) {
	ref := p.rep.sessionReference()
	ref.path = ctx.GetSessionFile()
	if !absolutePath(ref.path) {
		ref.path = ""
	}
	ref.id = ctx.GetSessionID()
	p.rep.setSessionReference(ref)
}

// herdrPane reports the pane herdr launched this session for. Herdr sets these
// only in the panes it owns, so an ordinary terminal keeps reporting off.
func herdrPane() (socketPath, paneID string, ok bool) {
	if os.Getenv("HERDR_ENV") != "1" {
		return "", "", false
	}
	socketPath = os.Getenv("HERDR_SOCKET_PATH")
	paneID = os.Getenv("HERDR_PANE_ID")
	if socketPath == "" || paneID == "" {
		return "", "", false
	}
	return socketPath, paneID, true
}

// absolutePath mirrors Pi's `path.posix.isAbsolute(file) || path.win32.isAbsolute(file)`:
// a session written on another platform still identifies the transcript herdr
// should open.
func absolutePath(path string) bool {
	switch {
	case strings.HasPrefix(path, "/"):
		return true
	case strings.HasPrefix(path, `\\`):
		return true
	case len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/'):
		return true
	default:
		return false
	}
}

// logf reports something that changed what herdr shows, or a host answer this
// extension could not use. These are rare, one line each, and go to stderr,
// which PiG collects as the extension's output.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, extensionName+": "+format+"\n", args...)
}
