// Package host is the AHP host: message routing, subscriptions, sequencing, broadcast and
// the authoritative channel state. It is a port of pi-ahp's src/core (host.ts, sequencer.ts,
// state-store.ts, connection.ts, client-workarounds.ts) and knows nothing about PiG.
//
// Concurrency: the host serializes its own state with one mutex. Handlers registered
// through Capabilities and listeners run outside it, so they may call back into the host.
package host

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/microsoft/agent-host-protocol/clients/go/ahptypes"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Transport is one reliable, ordered, message-framed stream. Send must not block on a slow
// peer (the WebSocket transport queues and closes a peer that falls behind): the host
// broadcasts from inside its critical section.
type Transport interface {
	Send(frame []byte) error
	Close() error
}

// Options configure a Host.
type Options struct {
	// ServerInfo is advertised on InitializeResult.serverInfo. Informational only.
	ServerInfo *ahptypes.Implementation
	// DefaultDirectory is the starting location for remote filesystem browsing, as a file: URI.
	DefaultDirectory string
	// CompletionTriggerCharacters make a client issue a completions request.
	CompletionTriggerCharacters []string
	// ReplayBufferCapacity bounds the replay buffer; 0 selects the default (1000).
	ReplayBufferCapacity int
	Log                  func(string)
}

// Capabilities are the optional protocol surfaces. A request for an absent surface receives an
// empty result where meaningful and MethodNotFound otherwise.
type Capabilities struct {
	Catalogue       SessionCatalogue
	Sessions        SessionLifecycle
	Terminals       TerminalHandler
	Resources       ResourceHandler
	ResourceWatches ResourceWatchHandler
	Completions     CompletionHandler
	SessionConfig   SessionConfigHandler
	TurnPaging      TurnPagingHandler
	Hydrator        ChannelHydrator
}

// SessionCatalogue supplies the session list behind listSessions.
type SessionCatalogue interface {
	List(ctx context.Context, limit *int64, cursor *string) (ahptypes.ListSessionsResult, error)
}

// SessionLifecycle handles createSession and disposeSession.
type SessionLifecycle interface {
	Create(ctx context.Context, params ahptypes.CreateSessionParams) error
	Dispose(ctx context.Context, channel string) error
}

// TerminalHandler handles createTerminal and disposeTerminal.
type TerminalHandler interface {
	Create(ctx context.Context, params ahptypes.CreateTerminalParams, clientID string) error
	Dispose(ctx context.Context, channel string) error
}

// ResourceHandler serves the host side of the connection-level resource* family.
type ResourceHandler interface {
	Read(ctx context.Context, p ahptypes.ResourceReadParams) (ahptypes.ResourceReadResult, error)
	Write(ctx context.Context, p ahptypes.ResourceWriteParams) error
	List(ctx context.Context, uri string) (ahptypes.ResourceListResult, error)
	Resolve(ctx context.Context, p ahptypes.ResourceResolveParams) (ahptypes.ResourceResolveResult, error)
	Mkdir(ctx context.Context, p ahptypes.ResourceMkdirParams) error
	Delete(ctx context.Context, p ahptypes.ResourceDeleteParams) error
	Move(ctx context.Context, p ahptypes.ResourceMoveParams) error
	Copy(ctx context.Context, p ahptypes.ResourceCopyParams) error
}

// ResourceWatchHandler opens filesystem watchers.
type ResourceWatchHandler interface {
	Create(ctx context.Context, p ahptypes.CreateResourceWatchParams) (ahptypes.CreateResourceWatchResult, error)
}

// CompletionHandler serves inline completions for a chat's message input.
type CompletionHandler interface {
	Complete(ctx context.Context, p ahptypes.CompletionsParams) (ahptypes.CompletionsResult, error)
}

// SessionConfigHandler serves the pre-creation session configuration exchange.
type SessionConfigHandler interface {
	Resolve(ctx context.Context, p ahptypes.ResolveSessionConfigParams) (ahptypes.ResolveSessionConfigResult, error)
	Completions(ctx context.Context, p ahptypes.SessionConfigCompletionsParams) (ahptypes.SessionConfigCompletionsResult, error)
}

// TurnPagingHandler loads older turns into a chat; the page is dispatched before it returns.
type TurnPagingHandler interface {
	FetchTurns(ctx context.Context, p ahptypes.FetchTurnsParams) error
}

// ChannelHydrator materializes a channel that exists durably but is not in memory.
type ChannelHydrator interface {
	Hydrate(ctx context.Context, channel string) (bool, error)
}

// ClientActionListener is a post-commit hook for actions a client dispatched.
type ClientActionListener func(channel string, action ahptypes.StateAction)

// CommittedActionListener observes every accepted action after it was reduced and broadcast.
type CommittedActionListener func(channel string, action ahptypes.StateAction)

// ClientActionValidator returns a reason to refuse a client action, or "" to accept it.
type ClientActionValidator func(channel string, action ClientAction, clientID string) string

// SubscriberCountListener is told when the number of clients subscribed to a channel changes.
type SubscriberCountListener func(channel string, count int)

// defaultReplayBufferCapacity is the local memory bound for replay. A client whose gap predates
// the buffer gets fresh snapshots instead, preserving correctness at the cost of more data.
const defaultReplayBufferCapacity = 1000

// ProviderScheme is the provider id of the agent this host advertises; VS Code derives its
// session URIs from it.
const ProviderScheme = "pi"

// Host is the AHP host.
type Host struct {
	opts  Options
	store *Store

	mu    sync.Mutex // guards everything below except the event queue
	seq   int64
	buf   []ahptypes.ActionEnvelope
	cap   int
	conns []*Conn // insertion order
	// clientInfoByID: reconnect omits clientInfo; a present key with "" still records an id seen by this host process.
	clientInfoByID map[string]string
	caps           Capabilities

	listenMu        sync.Mutex
	actionListeners []*listener[ClientActionListener]
	commitListeners []*listener[CommittedActionListener]
	validators      []*listener[ClientActionValidator]
	countListeners  []*listener[SubscriberCountListener]

	evMu     sync.Mutex
	events   []func()
	draining bool
}

type listener[T any] struct{ fn T }

// New returns a host with no channels; install the root channel before clients connect.
func New(opts Options) *Host {
	c := opts.ReplayBufferCapacity
	if c <= 0 {
		c = defaultReplayBufferCapacity
	}
	return &Host{opts: opts, store: newStore(), cap: c, clientInfoByID: map[string]string{}}
}

// Store returns the authoritative channel state.
func (h *Host) Store() *Store { return h.store }

// ServerSeq is the seq of the most recently emitted action envelope (0 before anything is emitted).
func (h *Host) ServerSeq() int64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.seq
}

// Serve declares what this host serves; it merges into earlier declarations.
func (h *Host) Serve(c Capabilities) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c.Catalogue != nil {
		h.caps.Catalogue = c.Catalogue
	}
	if c.Sessions != nil {
		h.caps.Sessions = c.Sessions
	}
	if c.Terminals != nil {
		h.caps.Terminals = c.Terminals
	}
	if c.Resources != nil {
		h.caps.Resources = c.Resources
	}
	if c.ResourceWatches != nil {
		h.caps.ResourceWatches = c.ResourceWatches
	}
	if c.Completions != nil {
		h.caps.Completions = c.Completions
	}
	if c.SessionConfig != nil {
		h.caps.SessionConfig = c.SessionConfig
	}
	if c.TurnPaging != nil {
		h.caps.TurnPaging = c.TurnPaging
	}
	if c.Hydrator != nil {
		h.caps.Hydrator = c.Hydrator
	}
}

func (h *Host) capabilities() Capabilities {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.caps
}

func (h *Host) log(format string, args ...any) {
	if h.opts.Log != nil {
		h.opts.Log("[ahp-host] " + fmt.Sprintf(format, args...))
	}
}

// InstallRootChannel registers ahp-root:// with the given agents.
func (h *Host) InstallRootChannel(agents []ahptypes.AgentInfo) {
	if agents == nil {
		agents = []ahptypes.AgentInfo{}
	}
	zero := int64(0)
	_ = h.store.Create(wire.RootChannel, &ahptypes.RootState{Agents: agents, ActiveSessions: &zero})
}

// ── events (listeners run outside every lock, in commit order) ──────────

func (h *Host) emit(fn func()) {
	h.evMu.Lock()
	h.events = append(h.events, fn)
	h.evMu.Unlock()
}

// drain runs queued listener events. A re-entrant call (a listener dispatching another action)
// only enqueues; the outer drain runs the derived event after the current one returns, so a
// derived action is always sequenced and observed after the action that caused it.
func (h *Host) drain() {
	h.evMu.Lock()
	if h.draining {
		h.evMu.Unlock()
		return
	}
	h.draining = true
	for len(h.events) > 0 {
		fn := h.events[0]
		h.events[0] = nil
		h.events = h.events[1:]
		h.evMu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					h.log("listener panicked: %v", r)
				}
			}()
			fn()
		}()
		h.evMu.Lock()
	}
	h.draining = false
	h.evMu.Unlock()
}

func add[T any](h *Host, list *[]*listener[T], fn T) func() {
	l := &listener[T]{fn: fn}
	h.listenMu.Lock()
	*list = append(*list, l)
	h.listenMu.Unlock()
	return func() {
		h.listenMu.Lock()
		defer h.listenMu.Unlock()
		for i, x := range *list {
			if x == l {
				*list = append((*list)[:i:i], (*list)[i+1:]...)
				return
			}
		}
	}
}

func snapshotListeners[T any](h *Host, list []*listener[T]) []T {
	h.listenMu.Lock()
	defer h.listenMu.Unlock()
	out := make([]T, len(list))
	for i, l := range list {
		out[i] = l.fn
	}
	return out
}

// OnClientAction registers a post-commit hook for client-dispatched actions. Actions are applied and
// broadcast before the hook runs. Rejected actions never reach it.
func (h *Host) OnClientAction(l ClientActionListener) func() { return add(h, &h.actionListeners, l) }

// OnActionCommitted observes every accepted action after its envelope has been broadcast.
func (h *Host) OnActionCommitted(l CommittedActionListener) func() {
	return add(h, &h.commitListeners, l)
}

// AddClientActionValidator registers a pre-commit check, needed whenever accepting an action would
// leave the host unable to carry it out.
func (h *Host) AddClientActionValidator(v ClientActionValidator) func() {
	return add(h, &h.validators, v)
}

// OnSubscriberCountChanged observes changes to a channel's subscriber count.
func (h *Host) OnSubscriberCountChanged(l SubscriberCountListener) func() {
	return add(h, &h.countListeners, l)
}

// SubscriberCount is how many connected clients are subscribed to a channel.
func (h *Host) SubscriberCount(channel string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.subscriberCountLocked(channel)
}

func (h *Host) subscriberCountLocked(channel string) int {
	n := 0
	for _, c := range h.conns {
		if c.subscribed(channel) {
			n++
		}
	}
	return n
}

// notifyCountLocked queues a subscriber-count notification (run by drain, outside the lock).
func (h *Host) notifyCountLocked(channel string) {
	count := h.subscriberCountLocked(channel)
	for _, fn := range snapshotListeners(h, h.countListeners) {
		fn := fn
		h.emit(func() { fn(channel, count) })
	}
}

// DeleteChannel removes a channel and releases every connection subscribed to that identity.
func (h *Host) DeleteChannel(channel string) bool {
	h.mu.Lock()
	deleted := h.store.Delete(channel)
	released := false
	for _, c := range h.conns {
		if c.unsubscribeLocked(channel) {
			released = true
		}
	}
	if released {
		h.notifyCountLocked(channel)
	}
	h.mu.Unlock()
	h.drain()
	return deleted
}

// ── connections ─────────────────────────────────────────────────────────

// Conn is one accepted transport connection and its per-socket subscriptions.
type Conn struct {
	h         *Host
	transport Transport
	ctx       context.Context
	cancel    context.CancelFunc

	// guarded by h.mu
	clientID string
	subs     []string
	closed   bool

	workMu sync.Mutex // guards workarounds (used outside h.mu for incoming rewrites)
	work   *ClientWorkarounds
}

// Accept attaches a transport. The clientId is not known until initialize.
func (h *Host) Accept(t Transport) *Conn {
	ctx, cancel := context.WithCancel(context.Background())
	c := &Conn{h: h, transport: t, ctx: ctx, cancel: cancel, work: NewClientWorkarounds(ProviderScheme)}
	h.mu.Lock()
	h.conns = append(h.conns, c)
	h.mu.Unlock()
	return c
}

func (c *Conn) subscribed(channel string) bool {
	for _, s := range c.subs {
		if s == channel {
			return true
		}
	}
	return false
}

func (c *Conn) subscribeLocked(channel string) bool {
	if c.subscribed(channel) {
		return false
	}
	c.subs = append(c.subs, channel)
	return true
}

func (c *Conn) unsubscribeLocked(channel string) bool {
	for i, s := range c.subs {
		if s == channel {
			c.subs = append(c.subs[:i:i], c.subs[i+1:]...)
			return true
		}
	}
	return false
}

// Closed tells the host the transport went away; the connection's subscriptions are released just
// like explicit unsubscribes, since resources tied to them must not outlive the client.
func (c *Conn) Closed() {
	h := c.h
	h.mu.Lock()
	if c.closed {
		h.mu.Unlock()
		return
	}
	c.closed = true
	c.cancel()
	h.removeConnLocked(c)
	for _, channel := range c.subs {
		h.notifyCountLocked(channel)
	}
	h.mu.Unlock()
	h.drain()
}

func (h *Host) removeConnLocked(c *Conn) {
	for i, x := range h.conns {
		if x == c {
			h.conns = append(h.conns[:i:i], h.conns[i+1:]...)
			return
		}
	}
}

// sendLocked serializes and sends one message to this connection, applying its client workarounds.
func (c *Conn) sendLocked(msg any) {
	if c.closed {
		return
	}
	frame, err := json.Marshal(msg)
	if err != nil {
		c.h.log("cannot marshal outgoing message: %v", err)
		return
	}
	c.workMu.Lock()
	active := c.work.Active()
	c.workMu.Unlock()
	if active {
		var generic map[string]any
		dec := json.NewDecoder(bytes.NewReader(frame))
		dec.UseNumber()
		if err := dec.Decode(&generic); err == nil {
			c.workMu.Lock()
			generic = c.work.ApplyToOutgoing(generic)
			c.workMu.Unlock()
			if rewritten, err := json.Marshal(generic); err == nil {
				frame = rewritten
			}
		}
	}
	if err := c.transport.Send(frame); err != nil {
		c.h.log("send failed: %v", err)
	}
}

// ── message routing ─────────────────────────────────────────────────────

type request struct {
	id     json.RawMessage
	method string
	params map[string]any
}

// Receive handles one inbound text frame. Requests that may block run on their own goroutine so the
// connection's read loop stays live (a ping must always be answered); everything else runs inline,
// which keeps the order of a client's messages.
func (c *Conn) Receive(frame []byte) {
	var msg map[string]any
	dec := json.NewDecoder(bytes.NewReader(frame))
	dec.UseNumber()
	if err := dec.Decode(&msg); err != nil {
		// A frame we cannot parse has no id, so there is nobody to answer.
		c.h.log("dropping unparseable frame: %v", err)
		return
	}
	method, hasMethod := msg["method"].(string)
	_, hasID := msg["id"]
	idNumber, idIsNumber := msg["id"].(json.Number)
	switch {
	case hasMethod && hasID && idIsNumber:
		id := json.RawMessage(idNumber.String())
		c.handleRequest(id, method, msg)
	case hasMethod && !hasID:
		c.handleNotification(method, msg)
	default:
		text := string(frame)
		if len(text) > 200 {
			text = text[:200]
		}
		c.h.log("ignoring unroutable message: %s", text)
	}
}

func (c *Conn) respond(id json.RawMessage, result any, err error) {
	h := c.h
	h.mu.Lock()
	c.respondLocked(id, result, err)
	h.mu.Unlock()
	// Listener events queued while handling the request (subscriber counts) run once the response is sent.
	h.drain()
}

func (c *Conn) respondLocked(id json.RawMessage, result any, err error) {
	if err != nil {
		var pe *wire.Error
		if !errors.As(err, &pe) {
			pe = &wire.Error{Code: wire.CodeInternalError, Message: err.Error()}
		}
		c.h.log("request failed: %s", pe.Message)
		e := map[string]any{"code": pe.Code, "message": pe.Message}
		if pe.Data != nil {
			e["data"] = pe.Data
		}
		c.sendLocked(map[string]any{"jsonrpc": "2.0", "id": id, "error": e})
		return
	}
	if result == nil {
		result = json.RawMessage("null")
	}
	c.sendLocked(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func isRootCommand(method string) bool {
	switch method {
	case "initialize", "ping", "reconnect", "listSessions", "resourceRead", "resourceWrite", "resourceList",
		"resourceCopy", "resourceDelete", "resourceMove", "resourceResolve", "resourceMkdir", "resourceRequest",
		"createResourceWatch", "authenticate", "resolveSessionConfig", "sessionConfigCompletions", "listAutomationTriggerDefinitions":
		return true
	}
	return false
}

func (c *Conn) handleRequest(id json.RawMessage, method string, msg map[string]any) {
	h := c.h
	p, _ := msg["params"].(map[string]any)

	// Handshake and pre-handshake policy.
	h.mu.Lock()
	initialized := c.clientID != ""
	h.mu.Unlock()
	handshake := method == "initialize" || method == "reconnect"
	if handshake && initialized {
		c.respond(id, nil, &wire.Error{Code: wire.CodeInvalidRequest, Message: "Connection is already initialized"})
		return
	}
	if !handshake && method != "ping" && !initialized {
		c.respond(id, nil, &wire.Error{Code: wire.CodeInvalidRequest, Message: "initialize or reconnect must be the first request"})
		return
	}
	// Validate handshake shapes before any workaround observes them, so an invalid request cannot
	// establish a URI dialect.
	var initParams ahptypes.InitializeParams
	var reconnectParams ahptypes.ReconnectParams
	switch method {
	case "initialize":
		if err := decodeParams(p, &initParams); err != nil || initParams.ClientId == "" {
			c.respond(id, nil, wire.InvalidParams(invalidMessage("initialize requires a clientId", err)))
			return
		}
	case "reconnect":
		// VS Code omits channel in reconnect (AHP 0.9 discriminant): repaired by the workaround
		// below once VS Code is identified, so decode leniently first and validate after.
	}
	if err := c.applyWorkarounds(method, msg, p, initParams); err != nil {
		c.respond(id, nil, err)
		return
	}
	p, _ = msg["params"].(map[string]any)
	if isRootCommand(method) {
		if channel, _ := p["channel"].(string); channel != wire.RootChannel {
			c.respond(id, nil, wire.InvalidParams(method+" requires channel "+wire.RootChannel))
			return
		}
	}

	switch method {
	case "ping":
		// Answered whether or not the client has completed initialize or holds any subscription,
		// but like every connection-level command it must be routed on the root channel.
		c.respond(id, nil, nil)
	case "initialize":
		// Re-decode: the workarounds may have rewritten initialSubscriptions.
		initParams = ahptypes.InitializeParams{}
		if err := decodeParams(p, &initParams); err != nil {
			c.respond(id, nil, wire.InvalidParams(invalidMessage("invalid initialize params", err)))
			return
		}
		if _, err := wire.NegotiateProtocolVersion(initParams.ProtocolVersions); err != nil {
			c.respond(id, nil, err)
			return
		}
		c.runMaybeBlocking(id, c.needsHydration(initParams.InitialSubscriptions), func() (any, error) { return c.initialize(initParams) })
	case "reconnect":
		if err := decodeParams(p, &reconnectParams); err != nil || reconnectParams.ClientId == "" || reconnectParams.LastSeenServerSeq < 0 {
			c.respond(id, nil, wire.InvalidParams(invalidMessage("reconnect requires a clientId, a non-negative integer lastSeenServerSeq and a subscriptions array of URIs", err)))
			return
		}
		c.runMaybeBlocking(id, c.needsHydration(reconnectParams.Subscriptions), func() (any, error) { return c.reconnect(reconnectParams) })
	case "subscribe":
		var params ahptypes.SubscribeParams
		if err := decodeParams(p, &params); err != nil || params.Channel == "" {
			c.respond(id, nil, wire.InvalidParams("subscribe requires a channel"))
			return
		}
		c.runMaybeBlocking(id, c.needsHydration([]string{params.Channel}), func() (any, error) { return c.subscribe(params) })
	default:
		c.runBlocking(id, func() (any, error) { return c.dispatchOther(method, p) })
	}
}

func invalidMessage(base string, err error) string {
	if err == nil {
		return base
	}
	return base + " (" + err.Error() + ")"
}

func decodeParams(p map[string]any, into any) error {
	if p == nil {
		return errors.New("params are required")
	}
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

// applyWorkarounds identifies the client and rewrites the request in place.
func (c *Conn) applyWorkarounds(method string, msg, p map[string]any, init ahptypes.InitializeParams) error {
	h := c.h
	c.workMu.Lock()
	defer c.workMu.Unlock()
	switch method {
	case "initialize":
		name := ""
		if init.ClientInfo != nil {
			name = init.ClientInfo.Name
		}
		if name == "" {
			h.mu.Lock()
			name = h.clientInfoByID[init.ClientId]
			h.mu.Unlock()
		}
		c.work.Identify(name)
	case "reconnect":
		clientID, _ := p["clientId"].(string)
		h.mu.Lock()
		name := h.clientInfoByID[clientID]
		h.mu.Unlock()
		c.work.Identify(name)
	}
	if err := c.work.ApplyToIncoming(msg); err != nil {
		return err
	}
	return nil
}

// needsHydration reports whether a handshake or subscribe names a channel only the hydrator can supply.
func (c *Conn) needsHydration(channels []string) bool {
	caps := c.h.capabilities()
	if caps.Hydrator == nil {
		return false
	}
	for _, ch := range channels {
		if ch != wire.RootChannel && !c.h.store.Has(ch) {
			return true
		}
	}
	return false
}

func (c *Conn) runMaybeBlocking(id json.RawMessage, blocking bool, fn func() (any, error)) {
	if blocking {
		c.runBlocking(id, fn)
		return
	}
	result, err := fn()
	c.respond(id, result, err)
}

func (c *Conn) runBlocking(id json.RawMessage, fn func() (any, error)) {
	go func() {
		result, err := func() (result any, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("internal error: %v", r)
				}
			}()
			return fn()
		}()
		c.respond(id, result, err)
	}()
}

func (c *Conn) handleNotification(method string, msg map[string]any) {
	h := c.h
	h.mu.Lock()
	initialized := c.clientID != ""
	h.mu.Unlock()
	if !initialized {
		h.log("ignoring %s before initialize or reconnect", method)
		return
	}
	c.workMu.Lock()
	_ = c.work.ApplyToIncoming(msg)
	c.workMu.Unlock()
	p, _ := msg["params"].(map[string]any)
	switch method {
	case "unsubscribe":
		channel, ok := p["channel"].(string)
		if !ok {
			return
		}
		h.mu.Lock()
		if c.unsubscribeLocked(channel) {
			h.notifyCountLocked(channel)
		}
		h.mu.Unlock()
		h.drain()
	case "dispatchAction":
		c.dispatchClientAction(p)
	default:
		h.log("ignoring unknown notification: %s", method)
	}
}

// ── handshake ───────────────────────────────────────────────────────────

func (c *Conn) hydrate(channels []string) {
	caps := c.h.capabilities()
	if caps.Hydrator == nil {
		return
	}
	for _, ch := range channels {
		if ch != wire.RootChannel && !c.h.store.Has(ch) {
			if _, err := caps.Hydrator.Hydrate(c.ctx, ch); err != nil {
				c.h.log("hydrate %s failed: %v", ch, err)
			}
		}
	}
}

func (c *Conn) initialize(p ahptypes.InitializeParams) (any, error) {
	version, err := wire.NegotiateProtocolVersion(p.ProtocolVersions)
	if err != nil {
		return nil, err
	}
	h := c.h
	// Same lazy load as subscribe: a client reconnecting with its previously-open sessions must get them back.
	c.hydrate(p.InitialSubscriptions)

	h.mu.Lock()
	defer h.mu.Unlock()
	if c.clientID != "" {
		return nil, &wire.Error{Code: wire.CodeInvalidRequest, Message: "Connection is already initialized"}
	}
	h.bindClientLocked(c, p.ClientId)
	name := ""
	if p.ClientInfo != nil {
		name = p.ClientInfo.Name
	}
	if name != "" || !h.knownClientLocked(p.ClientId) {
		h.clientInfoByID[p.ClientId] = name
	}

	snapshots := []ahptypes.Snapshot{}
	for _, uri := range p.InitialSubscriptions {
		if snap, ok := h.trySubscribeLocked(c, uri); ok {
			snapshots = append(snapshots, snap)
		}
	}
	result := ahptypes.InitializeResult{
		ProtocolVersion: version,
		ServerSeq:       h.seq,
		ServerInfo:      h.opts.ServerInfo,
		Snapshots:       snapshots,
	}
	if h.opts.DefaultDirectory != "" {
		d := h.opts.DefaultDirectory
		result.DefaultDirectory = &d
	}
	if len(h.opts.CompletionTriggerCharacters) > 0 {
		result.CompletionTriggerCharacters = append([]string(nil), h.opts.CompletionTriggerCharacters...)
	}
	return result, nil
}

func (h *Host) knownClientLocked(clientID string) bool {
	_, ok := h.clientInfoByID[clientID]
	return ok
}

// bindClientLocked replaces a half-open socket when the same clientId reconnects.
func (h *Host) bindClientLocked(c *Conn, clientID string) {
	for _, previous := range append([]*Conn(nil), h.conns...) {
		if previous != c && previous.clientID == clientID {
			h.removeConnLocked(previous)
			for _, channel := range previous.subs {
				h.notifyCountLocked(channel)
			}
			previous.subs = nil
			previous.closed = true
			previous.cancel()
			go previous.transport.Close()
		}
	}
	c.clientID = clientID
}

func (c *Conn) reconnect(p ahptypes.ReconnectParams) (any, error) {
	h := c.h
	c.hydrate(p.Subscriptions)

	h.mu.Lock()
	defer h.mu.Unlock()
	if c.clientID != "" {
		return nil, &wire.Error{Code: wire.CodeInvalidRequest, Message: "Connection is already initialized"}
	}
	known := h.knownClientLocked(p.ClientId)
	if !known {
		h.clientInfoByID[p.ClientId] = ""
	}
	h.bindClientLocked(c, p.ClientId)

	missing := []string{}
	previous := c.subs
	c.subs = nil
	for _, uri := range p.Subscriptions {
		if uri == wire.RootChannel || h.store.Has(uri) {
			c.subscribeLocked(uri)
		} else {
			missing = append(missing, uri)
		}
	}
	for _, channel := range previous {
		if !c.subscribed(channel) {
			h.notifyCountLocked(channel)
		}
	}
	for _, channel := range c.subs {
		if !contains(previous, channel) {
			h.notifyCountLocked(channel)
		}
	}

	if known && h.canReplayFromLocked(p.LastSeenServerSeq) {
		return reconnectReplay{Type: "replay", Actions: h.replayFromLocked(p.LastSeenServerSeq, c.subs), Missing: missing}, nil
	}
	// Replay is unavailable after buffer eviction or in a fresh host process. Durable subscriptions
	// were hydrated above; return current snapshots.
	snapshots := []ahptypes.Snapshot{}
	for _, uri := range c.subs {
		if snap, ok := h.store.Snapshot(uri, h.seq); ok {
			snapshots = append(snapshots, snap)
		}
	}
	return reconnectSnapshot{Type: "snapshot", Snapshots: snapshots}, nil
}

// The vendored client's ReconnectReplayResult / ReconnectSnapshotResult carry no "type" discriminator
// on marshal (the union wrapper only marshals its value), which the wire format requires.
type reconnectReplay struct {
	Type    string                    `json:"type"`
	Actions []ahptypes.ActionEnvelope `json:"actions"`
	Missing []string                  `json:"missing"`
}

type reconnectSnapshot struct {
	Type      string              `json:"type"`
	Snapshots []ahptypes.Snapshot `json:"snapshots"`
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// canReplayFromLocked reports whether the gap after lastSeen is fully covered by the buffer. A client
// that is already current trivially qualifies; otherwise the buffer must still hold lastSeen+1.
func (h *Host) canReplayFromLocked(lastSeen int64) bool {
	if lastSeen >= h.seq {
		return true
	}
	return len(h.buf) > 0 && h.buf[0].ServerSeq <= lastSeen+1
}

func (h *Host) replayFromLocked(lastSeen int64, channels []string) []ahptypes.ActionEnvelope {
	out := []ahptypes.ActionEnvelope{}
	for _, env := range h.buf {
		if env.ServerSeq > lastSeen && contains(channels, env.Channel) {
			out = append(out, env)
		}
	}
	return out
}

// ── subscriptions ───────────────────────────────────────────────────────

func (c *Conn) subscribe(p ahptypes.SubscribeParams) (any, error) {
	h := c.h
	channel := p.Channel
	if !h.store.Has(channel) {
		// Not in memory does not mean it does not exist: a session from the catalogue lives on disk
		// until someone opens it. The hydrator gets the first chance to resolve known aliases before
		// the strict scheme check.
		hydrated := false
		if caps := h.capabilities(); caps.Hydrator != nil {
			ok, err := caps.Hydrator.Hydrate(c.ctx, channel)
			hydrated = err == nil && ok
		}
		if !hydrated && !h.store.Has(channel) {
			if _, known := wire.KindOf(channel); !known {
				return nil, wire.InvalidParams("Unsupported channel scheme: " + channel)
			}
			return nil, wire.NotFound(channel)
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if snap, ok := h.trySubscribeLocked(c, channel); ok {
		return ahptypes.SubscribeResult{Snapshot: &snap}, nil
	}
	return ahptypes.SubscribeResult{}, nil
}

func (h *Host) trySubscribeLocked(c *Conn, channel string) (ahptypes.Snapshot, bool) {
	// Membership in the store is the real test: a session opened at a non-standard URI has no
	// recognised scheme but is a perfectly valid channel.
	snap, ok := h.store.Snapshot(channel, h.seq)
	if !ok {
		h.log("ignoring subscription to unknown channel: %s", channel)
		return ahptypes.Snapshot{}, false
	}
	if c.subscribeLocked(channel) {
		h.notifyCountLocked(channel)
	}
	return snap, true
}

// ── actions ─────────────────────────────────────────────────────────────

func (c *Conn) dispatchClientAction(p map[string]any) {
	h := c.h
	channel, okChannel := p["channel"].(string)
	clientSeqNum, okSeq := p["clientSeq"].(json.Number)
	rawAction, okAction := p["action"].(map[string]any)
	actionType, okType := "", false
	if okAction {
		actionType, okType = rawAction["type"].(string)
	}
	if !okChannel || !okSeq || !okType {
		h.log("ignoring malformed dispatchAction")
		return
	}
	clientSeq, err := clientSeqNum.Int64()
	if err != nil {
		h.log("ignoring malformed dispatchAction")
		return
	}
	// Spec: an action naming a channel that does not exist is silently ignored — no echo, no rejection.
	if !h.store.Has(channel) {
		h.log("ignoring action for unknown channel: %s", channel)
		return
	}
	rawJSON, _ := json.Marshal(rawAction)
	var action ahptypes.StateAction
	decodeErr := json.Unmarshal(rawJSON, &action)
	if action.Value == nil {
		action = ahptypes.StateAction{Value: &ahptypes.StateActionUnknown{Raw: rawJSON}}
	}

	c.h.mu.Lock()
	clientID := c.clientID
	c.h.mu.Unlock()
	origin := &ahptypes.ActionOrigin{ClientId: clientID, ClientSeq: clientSeq}

	if !wire.IsClientDispatchable(actionType) {
		h.reject(channel, action, origin, "Action is not client-dispatchable: "+actionType)
		return
	}
	if kind, ok := h.store.KindOf(channel); ok && !wire.ActionBelongsToChannel(actionType, kind) {
		h.reject(channel, action, origin, actionType+" does not belong on a "+string(kind)+" channel")
		return
	}
	for _, validate := range snapshotListeners(h, h.validators) {
		if reason := validate(channel, ClientAction{Type: actionType, Raw: rawJSON, Action: action, DecodeErr: decodeErr}, clientID); reason != "" {
			h.reject(channel, action, origin, reason)
			return
		}
	}
	if decodeErr != nil {
		h.reject(channel, action, origin, "Malformed action: "+decodeErr.Error())
		return
	}
	h.commit(channel, action, origin)
	for _, fn := range snapshotListeners(h, h.actionListeners) {
		fn := fn
		h.emit(func() { fn(channel, action) })
	}
	h.drain()
}

// DispatchServerAction applies a host-originated action and broadcasts it: the single write path
// for everything the agent backend produces.
func (h *Host) DispatchServerAction(channel string, action ahptypes.StateAction) {
	if !h.store.Has(channel) {
		h.log("dropping server action for unknown channel: %s", channel)
		return
	}
	h.commit(channel, action, nil)
	h.drain()
}

func (h *Host) commit(channel string, action ahptypes.StateAction, origin *ahptypes.ActionOrigin) {
	h.mu.Lock()
	h.store.Apply(channel, action)
	h.seq++
	env := ahptypes.ActionEnvelope{Channel: channel, Action: action, ServerSeq: h.seq, Origin: origin}
	h.retainLocked(env)
	h.broadcastLocked(channel, "action", env)
	for _, fn := range snapshotListeners(h, h.commitListeners) {
		fn := fn
		h.emit(func() { fn(channel, action) })
	}
	h.mu.Unlock()
}

// reject echoes a rejected action so the write-ahead client can roll it back.
func (h *Host) reject(channel string, action ahptypes.StateAction, origin *ahptypes.ActionOrigin, reason string) {
	h.mu.Lock()
	h.seq++
	env := ahptypes.ActionEnvelope{Channel: channel, Action: action, ServerSeq: h.seq, Origin: origin, RejectionReason: &reason}
	h.retainLocked(env)
	h.broadcastLocked(channel, "action", env)
	h.mu.Unlock()
}

func (h *Host) retainLocked(env ahptypes.ActionEnvelope) {
	h.buf = append(h.buf, env)
	if len(h.buf) > h.cap {
		h.buf[0] = ahptypes.ActionEnvelope{}
		h.buf = h.buf[1:]
	}
}

func (h *Host) broadcastLocked(channel, method string, params any) {
	for _, c := range append([]*Conn(nil), h.conns...) {
		if c.subscribed(channel) {
			c.sendLocked(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
		}
	}
}

// Notify emits a protocol notification (root/sessionAdded, auth/required, ...) to the channel's
// subscribers. Notifications are ephemeral: not stored and not replayed on reconnect.
func (h *Host) Notify(channel, method string, params map[string]any) {
	full := map[string]any{"channel": channel}
	for k, v := range params {
		full[k] = v
	}
	h.mu.Lock()
	h.broadcastLocked(channel, method, full)
	h.mu.Unlock()
}

// ClientAction is a client-dispatched action as a validator sees it: the raw JSON (so a validator
// can report the precise malformed field), the typed action when it decoded, and the decode error.
type ClientAction struct {
	Type      string
	Raw       json.RawMessage
	Action    ahptypes.StateAction
	DecodeErr error
}

// ── other commands ──────────────────────────────────────────────────────

func (c *Conn) dispatchOther(method string, p map[string]any) (any, error) {
	h := c.h
	caps := h.capabilities()
	ctx := c.ctx
	switch method {
	case "listSessions":
		var params ahptypes.ListSessionsParams
		_ = decodeParams(p, &params)
		if caps.Catalogue == nil {
			return ahptypes.ListSessionsResult{Items: []ahptypes.SessionSummary{}}, nil
		}
		res, err := caps.Catalogue.List(ctx, params.Limit, params.Cursor)
		if res.Items == nil {
			res.Items = []ahptypes.SessionSummary{}
		}
		return res, err
	case "createSession":
		if caps.Sessions == nil {
			return nil, wire.MethodNotFound("createSession")
		}
		var params ahptypes.CreateSessionParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid createSession params", err))
		}
		return nil, caps.Sessions.Create(ctx, params)
	case "disposeSession":
		if caps.Sessions == nil {
			return nil, wire.MethodNotFound("disposeSession")
		}
		channel, ok := p["channel"].(string)
		if !ok || channel == "" {
			return nil, wire.InvalidParams("disposeSession requires a channel")
		}
		if err := h.assertCompatibleChannel(channel, wire.KindSession, "disposeSession"); err != nil {
			return nil, err
		}
		return nil, caps.Sessions.Dispose(ctx, channel)
	case "createTerminal":
		if caps.Terminals == nil {
			return nil, wire.MethodNotFound("createTerminal")
		}
		var params ahptypes.CreateTerminalParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid createTerminal params", err))
		}
		h.mu.Lock()
		clientID := c.clientID
		h.mu.Unlock()
		return nil, caps.Terminals.Create(ctx, params, clientID)
	case "disposeTerminal":
		if caps.Terminals == nil {
			return nil, wire.MethodNotFound("disposeTerminal")
		}
		channel, ok := p["channel"].(string)
		if !ok || channel == "" {
			return nil, wire.InvalidParams("disposeTerminal requires a channel")
		}
		if err := h.assertCompatibleChannel(channel, wire.KindTerminal, "disposeTerminal"); err != nil {
			return nil, err
		}
		return nil, caps.Terminals.Dispose(ctx, channel)
	case "resourceRead", "resourceWrite", "resourceList", "resourceResolve", "resourceMkdir", "resourceDelete", "resourceMove", "resourceCopy":
		return c.resource(ctx, caps, method, p)
	case "resolveSessionConfig":
		// A client calls this before createSession; answering MethodNotFound stops it from getting as far as creating one.
		if caps.SessionConfig == nil {
			return ahptypes.ResolveSessionConfigResult{Schema: ahptypes.SessionConfigSchema{Type: "object", Properties: map[string]ahptypes.SessionConfigPropertySchema{}}, Values: map[string]json.RawMessage{}}, nil
		}
		var params ahptypes.ResolveSessionConfigParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resolveSessionConfig params", err))
		}
		return caps.SessionConfig.Resolve(ctx, params)
	case "sessionConfigCompletions":
		if caps.SessionConfig == nil {
			return ahptypes.SessionConfigCompletionsResult{Items: []ahptypes.SessionConfigValueItem{}}, nil
		}
		var params ahptypes.SessionConfigCompletionsParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid sessionConfigCompletions params", err))
		}
		return caps.SessionConfig.Completions(ctx, params)
	case "fetchTurns":
		// The result carries no turns: the host must dispatch chat/turnsLoaded before responding, so the
		// client's state already holds the page by the time this returns.
		if caps.TurnPaging == nil {
			return map[string]any{}, nil
		}
		if err := h.assertChatChannel(p, "fetchTurns"); err != nil {
			return nil, err
		}
		var params ahptypes.FetchTurnsParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid fetchTurns params", err))
		}
		if err := caps.TurnPaging.FetchTurns(ctx, params); err != nil {
			return nil, err
		}
		return map[string]any{}, nil
	case "completions":
		// Best-effort by contract: a client debounces keystrokes into this, so an unconfigured host answers with nothing rather than an error.
		if caps.Completions == nil {
			return ahptypes.CompletionsResult{Items: []ahptypes.CompletionItem{}}, nil
		}
		if err := h.assertChatChannel(p, "completions"); err != nil {
			return nil, err
		}
		var params ahptypes.CompletionsParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid completions params", err))
		}
		return caps.Completions.Complete(ctx, params)
	case "createResourceWatch":
		if caps.ResourceWatches == nil {
			return nil, wire.MethodNotFound("createResourceWatch")
		}
		var params ahptypes.CreateResourceWatchParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid createResourceWatch params", err))
		}
		return caps.ResourceWatches.Create(ctx, params)
	case "resourceRequest":
		// No per-resource grants are tracked: a client that reaches this endpoint already holds the
		// token and can start a session, so a grant ledger here would imply a boundary that does not
		// exist. The receiver may still refuse individual operations.
		return map[string]any{}, nil
	}
	return nil, wire.MethodNotFound(method)
}

func (h *Host) assertCompatibleChannel(channel string, expected wire.ChannelKind, method string) error {
	if actual, ok := h.store.KindOf(channel); ok && actual != expected {
		return wire.InvalidParams(method + " cannot target a " + string(actual) + " channel")
	}
	return nil
}

func (h *Host) assertChatChannel(p map[string]any, method string) error {
	channel, ok := p["channel"].(string)
	if !ok || channel == "" {
		return wire.InvalidParams(method + " requires a channel")
	}
	kind, known := h.store.KindOf(channel)
	if !known {
		kind, known = wire.KindOf(channel)
	}
	if !known || kind != wire.KindChat {
		return wire.InvalidParams(method + " requires a chat channel")
	}
	return nil
}

func (c *Conn) resource(ctx context.Context, caps Capabilities, method string, p map[string]any) (any, error) {
	if caps.Resources == nil {
		return nil, wire.MethodNotFound("resource*")
	}
	r := caps.Resources
	switch method {
	case "resourceRead":
		var params ahptypes.ResourceReadParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceRead params", err))
		}
		return r.Read(ctx, params)
	case "resourceWrite":
		var params ahptypes.ResourceWriteParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceWrite params", err))
		}
		return map[string]any{}, r.Write(ctx, params)
	case "resourceList":
		uri, _ := p["uri"].(string)
		return r.List(ctx, uri)
	case "resourceResolve":
		var params ahptypes.ResourceResolveParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceResolve params", err))
		}
		return r.Resolve(ctx, params)
	case "resourceMkdir":
		var params ahptypes.ResourceMkdirParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceMkdir params", err))
		}
		return map[string]any{}, r.Mkdir(ctx, params)
	case "resourceDelete":
		var params ahptypes.ResourceDeleteParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceDelete params", err))
		}
		return map[string]any{}, r.Delete(ctx, params)
	case "resourceMove":
		var params ahptypes.ResourceMoveParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceMove params", err))
		}
		return map[string]any{}, r.Move(ctx, params)
	case "resourceCopy":
		var params ahptypes.ResourceCopyParams
		if err := decodeParams(p, &params); err != nil {
			return nil, wire.InvalidParams(invalidMessage("invalid resourceCopy params", err))
		}
		return map[string]any{}, r.Copy(ctx, params)
	}
	return nil, wire.MethodNotFound(method)
}
