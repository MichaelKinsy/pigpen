package herdragentstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// report is one herdr request: an id, the RPC method and its params, exactly the
// line the extension writes to herdr's pane socket.
type report struct {
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params"`
	// kind is queue bookkeeping and never reaches the wire.
	kind reportKind `json:"-"`
}

// Herdr takes one connection per request: it reads a single newline-terminated
// JSON object and answers with at least one byte when it accepted the request.
const (
	// firstAttempt bounds the first delivery attempt. A pane that is momentarily
	// busy answers or refuses within this window.
	firstAttempt = 500 * time.Millisecond
	// retryAttempt bounds the second attempt, made for the report herdr most
	// likely lost: herdr restarting under the pane, or the pane connecting while
	// herdr was still coming up.
	retryAttempt = 1500 * time.Millisecond
)

// delivery is the outcome of one send, kept for the diagnostics. Every field is
// filled from what actually happened on the socket, including the reason an
// attempt failed, which is the first thing to look for when herdr does not
// react: a wrong path, a refused connection, and silence are all failures, and
// they are not the same bug.
type delivery struct {
	method    string
	delivered bool
	attempts  int
	elapsed   time.Duration
	// reason explains the final attempt: a dial error, a write error, a closed
	// connection before any answer, or silence until the deadline.
	reason string
}

// String renders a refusal on one line. It is only printed when herdr did not
// acknowledge a report.
func (d delivery) String() string {
	return fmt.Sprintf("herdr did not accept %s after %d attempt(s) in %s: %s",
		d.method, d.attempts, d.elapsed.Round(time.Millisecond), d.reason)
}

// client is the one-shot request client for herdr's pane socket.
type client struct {
	endpoint string
}

// newClient resolves herdr's socket path for this platform. Herdr passes a bare
// pipe name on Windows and a socket path everywhere else.
func newClient(socketPath string) *client {
	endpoint := socketPath
	if endpoint != "" && namedPipeHost() {
		endpoint = `\\.\pipe\` + endpoint
	}
	return &client{endpoint: endpoint}
}

// send delivers r and reports exactly what happened. One retry follows a failed
// attempt, because a report that describes a state herdr never saw leaves its
// indicator wrong until the next transition.
func (c *client) send(r report) delivery {
	start := time.Now()
	out := delivery{method: r.Method}

	delivered, reason := c.attempt(r, firstAttempt)
	out.attempts, out.reason = 1, reason
	if !delivered {
		delivered, reason = c.attempt(r, retryAttempt)
		out.attempts, out.reason = out.attempts+1, reason
	}
	out.delivered, out.elapsed = delivered, time.Since(start)
	return out
}

// attempt writes r to a fresh connection and waits for herdr's answer. The
// deadline is enforced by closing the connection rather than only by
// SetDeadline, so it also bounds the named-pipe path on Windows. Any reply byte
// means the report was accepted; a refused connection, a closed connection or
// silence means it was not.
func (c *client) attempt(r report, timeout time.Duration) (bool, string) {
	payload, err := json.Marshal(r)
	if err != nil {
		return false, fmt.Sprintf("encode %s: %v", r.Method, err)
	}
	payload = append(payload, '\n')

	start := time.Now()
	conn, err := dial(c.endpoint, timeout)
	if err != nil {
		// The common case when herdr is not listening for this pane: a path that
		// no longer exists, or one this host cannot reach. Report it instead of
		// discarding it.
		return false, fmt.Sprintf("dial %s: %v", c.endpoint, err)
	}
	defer func() { _ = conn.Close() }()

	// Best effort only: a connection that cannot carry deadlines is still
	// bounded, because closing it ends the pending read.
	_ = conn.SetDeadline(time.Now().Add(timeout))
	expire := time.AfterFunc(timeout, func() { _ = conn.Close() })
	defer expire.Stop()

	if _, err := conn.Write(payload); err != nil {
		return false, fmt.Sprintf("write after %s to %s: %v", time.Since(start).Round(time.Millisecond), c.endpoint, err)
	}

	answer := make([]byte, 4096)
	read, readErr := conn.Read(answer)
	switch {
	case read > 0:
		return true, ""
	case errors.Is(readErr, net.ErrClosed):
		return false, fmt.Sprintf("no answer within %s on %s", timeout, c.endpoint)
	case readErr != nil:
		return false, fmt.Sprintf("read from %s: %v", c.endpoint, readErr)
	default:
		return false, fmt.Sprintf("herdr closed %s without answering", c.endpoint)
	}
}
