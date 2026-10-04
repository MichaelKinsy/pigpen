package herdragentstate

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Herdr's pane RPC surface, as specified in herdr's "Add Herdr support to your
// agent" and socket-API documentation.
//
// Herdr attributes a pane's agent state to a (source, agent) pair, so these two
// values decide whether herdr shows the report at all:
//
//   - source must be stable, unique, and must NOT begin with `herdr:` - that
//     prefix belongs to herdr's own integrations. Porting herdr's Pi extension
//     kept its `herdr:pi` source, which competes with the authority herdr
//     already holds for Pi, and this is the one value a third-party integration
//     must not copy.
//   - agent is the name users see and must be this agent's own name, not a name
//     herdr already supports. It is `pig`, not `pi`: PiG is not Pi, and herdr
//     matches the agent field against its own agent registry.
const (
	methodReportSession = "pane.report_agent_session"
	methodReportAgent   = "pane.report_agent"

	wireAgent  = "pig"
	wireSource = "piglet:herdr-agent-state"
)

// reportKind separates the two report shapes for queue coalescing. The wire
// payload is the same JSON in both cases.
type reportKind int

const (
	reportSession reportKind = iota
	reportState
)

// sessionRef identifies the transcript herdr attaches a pane to. The on-disk
// path wins when the session has one; the session id identifies an in-memory
// session.
type sessionRef struct {
	path string
	id   string
}

// with copies the reference onto a params map. Pi omits an undefined reference
// rather than sending an empty string, and herdr reads the absence the same way.
func (ref sessionRef) with(params map[string]any) map[string]any {
	if ref.path != "" {
		params["agent_session_path"] = ref.path
	} else if ref.id != "" {
		params["agent_session_id"] = ref.id
	}
	return params
}

// reporter turns pane state into ordered herdr reports and delivers them one at
// a time.
//
// Delivery is owned work, not a fire-and-forget promise per state change: one
// goroutine owns the queue, only one socket is in flight at a time, a report
// that herdr refused is retried, and close drains what is still queued before
// the extension's process goes away. Handlers only enqueue, so a slow or absent
// herdr never stalls the agent.
type reporter struct {
	client *client
	paneID string

	mu      sync.Mutex
	ref     sessionRef
	queue   []report
	wake    chan struct{}
	running bool
	closing bool
	drained chan struct{}

	// seq orders reports within one pane. Herdr rejects a report whose seq does
	// not advance, so every report takes a fresh value from a millisecond base.
	seq atomic.Int64
}

func newReporter(client *client, paneID string) *reporter {
	r := &reporter{
		client:  client,
		paneID:  paneID,
		wake:    make(chan struct{}, 1),
		drained: make(chan struct{}),
	}
	r.seq.Store(time.Now().UnixMilli() * 1000)
	return r
}

// sessionReport reports the session this pane is running, which herdr needs
// before it can attach a pane to a transcript. startSource is herdr's
// session_start reason and is omitted when the host did not send one.
//
// The report builders below take the reporter's own lock to snapshot the session
// reference, so a caller must build a report before it takes that lock itself.
func (r *reporter) sessionReport(startSource string) report {
	seq := r.seq.Add(1)
	params := map[string]any{
		"pane_id": r.paneID,
		"source":  wireSource,
		"agent":   wireAgent,
		"seq":     seq,
	}
	if startSource != "" {
		params["session_start_source"] = startSource
	}
	rep := r.report("session", seq, methodReportSession, params)
	rep.kind = reportSession
	return rep
}

// stateReport reports the agent's state. Herdr's message parameter only explains
// a block, so it is not sent for these states.
func (r *reporter) stateReport(state agentState) report {
	seq := r.seq.Add(1)
	params := map[string]any{
		"pane_id": r.paneID,
		"source":  wireSource,
		"agent":   wireAgent,
		"state":   string(state),
		"seq":     seq,
	}
	rep := r.report(string(state), seq, methodReportAgent, params)
	rep.kind = reportState
	return rep
}

// report finishes a request: it snapshots the session reference and stamps an
// id herdr can correlate. The seq already distinguishes requests within a pane,
// so the id needs no randomness.
func (r *reporter) report(kind string, seq int64, method string, params map[string]any) report {
	r.mu.Lock()
	ref := r.ref
	r.mu.Unlock()

	return report{
		ID:     fmt.Sprintf("%s:%s:%d:%d", wireSource, kind, time.Now().UnixMilli(), seq),
		Method: method,
		Params: ref.with(params),
	}
}

// sessionReference returns the current session reference.
func (r *reporter) sessionReference() sessionRef {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ref
}

// setSessionReference replaces the session reference.
func (r *reporter) setSessionReference(ref sessionRef) {
	r.mu.Lock()
	r.ref = ref
	r.mu.Unlock()
}

// enqueue appends rep to the delivery queue and returns without waiting for the
// socket. Reports keep the order they were enqueued in, which is what puts a
// session report ahead of the state that follows it.
//
// A state report that is still queued behind an in-flight send is superseded by
// the next one, because herdr asks an integration to "send only the latest
// state" and drop the older ones: a queued backlog of states is never what the
// user needs to see, and each of them costs a socket round trip.
func (r *reporter) enqueue(rep report) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	if rep.kind == reportState && len(r.queue) > 0 && r.queue[len(r.queue)-1].kind == reportState {
		r.queue[len(r.queue)-1] = rep
	} else {
		r.queue = append(r.queue, rep)
	}
	start := !r.running
	if start {
		r.running = true
	}
	r.mu.Unlock()

	if start {
		go r.deliver()
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// deliver sends queued reports in order until the queue is empty and close has
// run. The client already made the second attempt, so a report herdr did not
// accept is recorded, logged and counted here.
func (r *reporter) deliver() {
	defer func() {
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()
		close(r.drained)
	}()

	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			closing := r.closing
			r.mu.Unlock()
			if closing {
				return
			}
			// A wake token is buffered, so a report enqueued between the
			// emptiness check and this receive is never missed.
			<-r.wake
			continue
		}
		next := r.queue[0]
		r.queue = r.queue[1:]
		r.mu.Unlock()

		if outcome := r.client.send(next); !outcome.delivered {
			// The client already made the second attempt; one line says what
			// herdr did or failed to do, because a pane herdr ignores otherwise
			// looks exactly like a quiet one.
			logf("%s", outcome)
		}
	}
}

// close stops accepting reports and waits for the queued ones to drain, so the
// final state reaches herdr instead of being lost with the process. A report
// still in flight after the drain timeout is left to herdr's own timeout: the
// extension's shutdown is not worth an unbounded wait.
func (r *reporter) close(drainTimeout time.Duration) {
	r.mu.Lock()
	if r.closing {
		r.mu.Unlock()
		return
	}
	r.closing = true
	running := r.running
	r.mu.Unlock()

	select {
	case r.wake <- struct{}{}:
	default:
	}
	if !running {
		return
	}

	expired := time.NewTimer(drainTimeout)
	defer expired.Stop()
	select {
	case <-r.drained:
	case <-expired.C:
		logf("reports still queued after %s; leaving them to herdr", drainTimeout)
	}
}
