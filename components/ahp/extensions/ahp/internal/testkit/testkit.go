// Package testkit is the test harness shared by the twin tests: an in-memory JSON-RPC
// client for a host.Host, the counterpart of upstream test/harness.ts (which drives a
// live host with the Microsoft AHP client). It is test support only and is not imported by
// the extension.
package testkit

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/host"
	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Timeout is how long a request or an awaited message may take before the test fails.
const Timeout = 2 * time.Second

// RPCError is a JSON-RPC error response.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

// Notification is a server notification received by a client.
type Notification struct {
	Method string
	Params json.RawMessage
}

// Envelope decodes an "action" notification.
func (n Notification) Envelope() (ahptypes.ActionEnvelope, bool) {
	var env ahptypes.ActionEnvelope
	if n.Method != "action" || json.Unmarshal(n.Params, &env) != nil {
		return env, false
	}
	return env, true
}

type memTransport struct{ c *Client }

func (m *memTransport) Send(frame []byte) error {
	m.c.deliver(append([]byte(nil), frame...))
	return nil
}

func (m *memTransport) Close() error {
	m.c.mu.Lock()
	m.c.serverClosed = true
	m.c.cond.Broadcast()
	m.c.mu.Unlock()
	return nil
}

// Client is an in-memory AHP client attached to a host.
type Client struct {
	t    *testing.T
	conn *host.Conn

	mu           sync.Mutex
	cond         *sync.Cond
	nextID       uint64
	clientSeq    int64
	pending      map[uint64]chan response
	notes        []Notification
	cursor       int
	serverClosed bool
}

type response struct {
	result json.RawMessage
	err    *RPCError
}

// Connect attaches a new client to the host.
func Connect(t *testing.T, h *host.Host) *Client {
	t.Helper()
	c := &Client{t: t, pending: map[uint64]chan response{}}
	c.cond = sync.NewCond(&c.mu)
	c.conn = h.Accept(&memTransport{c: c})
	return c
}

func (c *Client) deliver(frame []byte) {
	var msg struct {
		ID     *uint64         `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
		Result json.RawMessage `json:"result"`
		Error  *RPCError       `json:"error"`
	}
	if err := json.Unmarshal(frame, &msg); err != nil {
		c.t.Errorf("host sent an unparseable frame: %v: %s", err, frame)
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg.Method == "" && msg.ID != nil {
		if ch, ok := c.pending[*msg.ID]; ok {
			delete(c.pending, *msg.ID)
			ch <- response{result: msg.Result, err: msg.Error}
		}
		return
	}
	c.notes = append(c.notes, Notification{Method: msg.Method, Params: msg.Params})
	c.cond.Broadcast()
}

// Request sends a request and waits for its response.
func (c *Client) Request(method string, params any) (json.RawMessage, *RPCError) {
	c.t.Helper()
	ch := make(chan response, 1)
	c.mu.Lock()
	c.nextID++
	id := c.nextID
	c.pending[id] = ch
	c.mu.Unlock()
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		c.t.Fatalf("marshal %s: %v", method, err)
	}
	c.conn.Receive(frame)
	select {
	case r := <-ch:
		return r.result, r.err
	case <-time.After(Timeout):
		c.t.Fatalf("%s: no response within %v", method, Timeout)
		return nil, nil
	}
}

// Must returns a successful result or fails the test.
func (c *Client) Must(method string, params any) json.RawMessage {
	c.t.Helper()
	result, err := c.Request(method, params)
	if err != nil {
		c.t.Fatalf("%s: %v", method, err)
	}
	return result
}

// ExpectError requires the request to fail with the given code and returns the error.
func (c *Client) ExpectError(method string, params any, code int) *RPCError {
	c.t.Helper()
	result, err := c.Request(method, params)
	if err == nil {
		c.t.Fatalf("%s: expected error %d, got result %s", method, code, result)
	}
	if err.Code != code {
		c.t.Fatalf("%s: expected error %d, got %d: %s", method, code, err.Code, err.Message)
	}
	return err
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) {
	c.t.Helper()
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		c.t.Fatalf("marshal %s: %v", method, err)
	}
	c.conn.Receive(frame)
}

// Raw sends a frame exactly as given.
func (c *Client) Raw(frame []byte) { c.conn.Receive(frame) }

// Initialize performs the handshake with the supported versions and returns the result.
func (c *Client) Initialize(clientID string, extra map[string]any) ahptypes.InitializeResult {
	c.t.Helper()
	params := map[string]any{
		"channel": wire.RootChannel, "clientId": clientID,
		"protocolVersions": SupportedVersions(),
	}
	for k, v := range extra {
		params[k] = v
	}
	var result ahptypes.InitializeResult
	c.decode(c.Must("initialize", params), &result)
	return result
}

// SupportedVersions is SUPPORTED_PROTOCOL_VERSIONS of the pinned spec.
func SupportedVersions() []string {
	return []string{"0.9.0", "0.8.0", "0.7.0", "0.6.0", "0.5.2", "0.5.1"}
}

func (c *Client) decode(raw json.RawMessage, into any) {
	c.t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		c.t.Fatalf("decode %s into %T: %v", raw, into, err)
	}
}

// Decode decodes a result.
func (c *Client) Decode(raw json.RawMessage, into any) { c.t.Helper(); c.decode(raw, into) }

// Subscribe subscribes to a channel and returns the result.
func (c *Client) Subscribe(channel string) ahptypes.SubscribeResult {
	c.t.Helper()
	var result ahptypes.SubscribeResult
	c.decode(c.Must("subscribe", map[string]any{"channel": channel}), &result)
	return result
}

// Unsubscribe releases a channel.
func (c *Client) Unsubscribe(channel string) {
	c.t.Helper()
	c.Notify("unsubscribe", map[string]any{"channel": channel})
}

// Ping round-trips a ping; with in-order delivery it is a barrier for earlier notifications.
func (c *Client) Ping() { c.t.Helper(); c.Must("ping", map[string]any{"channel": wire.RootChannel}) }

// Dispatch sends a dispatchAction notification and returns its clientSeq. The action is any
// JSON-marshalable value (a typed action or a map).
func (c *Client) Dispatch(channel string, action any) int64 {
	c.t.Helper()
	c.mu.Lock()
	c.clientSeq++
	seq := c.clientSeq
	c.mu.Unlock()
	c.Notify("dispatchAction", map[string]any{"channel": channel, "clientSeq": seq, "action": action})
	return seq
}

// Notifications returns a copy of every notification received so far.
func (c *Client) Notifications() []Notification {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]Notification(nil), c.notes...)
}

// Await waits for the next unconsumed notification matching pred; ok is false on timeout.
func (c *Client) Await(pred func(Notification) bool, timeout time.Duration) (Notification, bool) {
	deadline := time.Now().Add(timeout)
	stop := time.AfterFunc(timeout, func() { c.mu.Lock(); c.cond.Broadcast(); c.mu.Unlock() })
	defer stop.Stop()
	c.mu.Lock()
	defer c.mu.Unlock()
	for {
		for c.cursor < len(c.notes) {
			n := c.notes[c.cursor]
			c.cursor++
			if pred(n) {
				return n, true
			}
		}
		if !time.Now().Before(deadline) {
			return Notification{}, false
		}
		c.cond.Wait()
	}
}

// NextEnvelope waits for the next action envelope on channel (any channel when empty) that
// matches clientSeq (any when 0).
func (c *Client) NextEnvelope(channel string, clientSeq int64) ahptypes.ActionEnvelope {
	c.t.Helper()
	n, ok := c.Await(func(n Notification) bool {
		env, isAction := n.Envelope()
		if !isAction {
			return false
		}
		return (channel == "" || env.Channel == channel) && (clientSeq == 0 || (env.Origin != nil && env.Origin.ClientSeq == clientSeq))
	}, Timeout)
	if !ok {
		c.t.Fatalf("timed out waiting for an action envelope on %q (clientSeq %d)", channel, clientSeq)
	}
	env, _ := n.Envelope()
	return env
}

// Silent reports whether no matching notification arrives within d.
func (c *Client) Silent(pred func(Notification) bool, d time.Duration) bool {
	_, ok := c.Await(pred, d)
	return !ok
}

// ServerClosed reports whether the host closed this client's transport.
func (c *Client) ServerClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.serverClosed
}

// Close simulates the transport going away.
func (c *Client) Close() { c.conn.Closed() }

// JSON marshals v, failing the test on error.
func JSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Normalize decodes JSON into generic values, with numbers as float64, for deep comparison.
func Normalize(t *testing.T, v any) any {
	t.Helper()
	var b []byte
	switch x := v.(type) {
	case []byte:
		b = x
	case json.RawMessage:
		b = x
	case string:
		b = []byte(x)
	default:
		var err error
		if b, err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	var out any
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("normalize %s: %v", b, err)
	}
	return out
}

// TestAgent is upstream's TEST_AGENT.
func TestAgent() ahptypes.AgentInfo {
	return ahptypes.AgentInfo{
		Provider: "pi", DisplayName: "pi", Description: "pi coding agent",
		Models: []ahptypes.SessionModelInfo{{Id: "test-model", Provider: "pi", Name: "Test Model"}},
	}
}

// NewHost returns a host with the root channel installed and TestAgent as its only agent.
func NewHost(opts host.Options) *host.Host {
	if opts.ServerInfo == nil {
		opts.ServerInfo = &ahptypes.Implementation{Name: "pi-ahp", Version: strPtr("0.0.1-test")}
	}
	h := host.New(opts)
	h.InstallRootChannel([]ahptypes.AgentInfo{TestAgent()})
	return h
}

func strPtr(s string) *string { return &s }

// Eventually polls cond until it holds or the timeout passes.
func Eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(Timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// ErrTimeout is returned by helpers that give up.
var ErrTimeout = errors.New("timed out")
