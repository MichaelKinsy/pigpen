package host

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/MichaelKinsy/pigpen/ahp/internal/wire"
)

// Repairs to the traffic of clients whose reading of the protocol differs from this host's. Each
// entry says what the client does and what would let it go. Port of src/core/client-workarounds.ts.
// Messages are handled as generic decoded JSON (map[string]any, numbers as json.Number or float64).

// rewritableFields carry session or chat URIs in state, actions and catalogue notifications.
var rewritableFields = map[string]bool{"channel": true, "resource": true, "defaultChat": true, "session": true, "chat": true}

// derivedChatURI is VS Code's derived chat URI: ahp-chat://<anything>/<base64url(sessionUri)>.
var derivedChatURI = regexp.MustCompile(`^ahp-chat://[^/]+/([^/?#]+)$`)

var vscodeClientNames = map[string]bool{"vscode-editor-window": true, "vscode-agents-window": true}

const vscodeMaterializedSessionDisposalRefusal = "Materialized session disposal is temporarily disabled for VS Code because of a VS Code provisional-session lifecycle bug; this session was kept."

// materializingActions are those after which a session is no longer an abandoned empty draft.
var materializingActions = map[string]bool{"chat/turnStarted": true, "chat/pendingMessageSet": true, "session/titleChanged": true}

// providerSessionMethods are the methods where a direct pi:/... target can only mean a session.
var providerSessionMethods = map[string]bool{
	"createSession": true, "disposeSession": true, "subscribe": true, "unsubscribe": true, "dispatchAction": true, "completions": true,
}

type uriDialect int

const (
	dialectCanonical uriDialect = iota
	dialectProvider
	dialectVSCode
)

// ClientWorkarounds translates one connection's traffic. VS Code computes provider-scoped session
// URIs and derived default-chat URIs instead of using the canonical resources published by the host.
// It also targets completions at the session URI rather than the chat URI. Publishing those shapes
// globally would impose one client's dialect on every client, so translation stays per connection
// and core services see canonical URIs.
type ClientWorkarounds struct {
	provider string
	isVSCode bool
	usesProv bool
	guard    *vscodeDisposalGuard
}

// NewClientWorkarounds returns a translator for the given provider scheme ("pi").
func NewClientWorkarounds(providerScheme string) *ClientWorkarounds {
	return &ClientWorkarounds{provider: providerScheme, guard: newVSCodeDisposalGuard(providerScheme)}
}

// Identify reads the implementation name given at initialize without erasing observations from wire traffic.
func (w *ClientWorkarounds) Identify(clientName string) {
	if vscodeClientNames[clientName] {
		w.isVSCode = true
	}
}

func (w *ClientWorkarounds) providerSessionID(uri string) (string, bool) {
	return wire.SessionIDFromURI(uri, w.provider)
}

func (w *ClientWorkarounds) isProviderSession(uri string) bool {
	return strings.HasPrefix(strings.ToLower(uri), strings.ToLower(w.provider)+":/") && func() bool { _, ok := w.providerSessionID(uri); return ok }()
}

func (w *ClientWorkarounds) canonicalSessionURI(uri string) (string, bool) {
	id, ok := w.providerSessionID(uri)
	if !ok {
		return "", false
	}
	return wire.SessionURI(id), true
}

func (w *ClientWorkarounds) owningSessionURI(uri string) (string, bool) {
	if s, ok := w.canonicalSessionURI(uri); ok {
		return s, true
	}
	if id, ok := wire.ChatIDFromURI(uri); ok {
		return wire.SessionURI(id), true
	}
	return "", false
}

func params(msg map[string]any) map[string]any {
	p, _ := msg["params"].(map[string]any)
	return p
}

func str(v any) (string, bool) { s, ok := v.(string); return s, ok }

func actionTypeOf(v any) string {
	m, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	s, _ := m["type"].(string)
	return s
}

// ApplyToIncoming rewrites a parsed request or notification in place. A *wire.Error result means
// the request must be refused with that error.
func (w *ClientWorkarounds) ApplyToIncoming(msg map[string]any) error {
	p := params(msg)
	if p == nil {
		return nil
	}
	method, _ := msg["method"].(string)
	_, hasID := msg["id"]
	w.observeTraffic(method, p)
	// Remove when VS Code includes the AHP 0.9 root-channel discriminant in reconnect.
	if hasID && w.isVSCode && method == "reconnect" {
		if _, present := p["channel"]; !present {
			p["channel"] = wire.RootChannel
		}
	}

	dialect := w.dialect()
	rewrite := func(uri string) string { return w.inbound(uri, dialect) }
	if channel, ok := str(p["channel"]); ok {
		channel = rewrite(channel)
		if method == "completions" && dialect != dialectCanonical {
			// Both VS Code and the iOS client currently target completions at the provider-style session URI.
			if id, ok := w.providerSessionID(channel); ok {
				channel = wire.ChatURI(id)
			}
		}
		// VS Code addresses its session rename to the selected chat. AHP defines session/titleChanged
		// only on the owning session.
		if method == "dispatchAction" && w.isVSCode && actionTypeOf(p["action"]) == "session/titleChanged" {
			if id, ok := wire.ChatIDFromURI(channel); ok {
				channel = wire.SessionURI(id)
			}
		}
		p["channel"] = channel
	}
	// Handshake requests name their subscribed channels in arrays rather than the routing channel.
	for _, field := range []string{"initialSubscriptions", "subscriptions"} {
		if list, ok := p[field].([]any); ok {
			out := make([]any, len(list))
			for i, item := range list {
				if s, ok := str(item); ok {
					out[i] = rewrite(s)
				} else {
					out[i] = item
				}
			}
			p[field] = out
		}
	}
	if w.isVSCode {
		return w.guard.applyToIncoming(msg, p)
	}
	return nil
}

// ApplyToOutgoing returns the outgoing message, rewritten when this client needs it.
func (w *ClientWorkarounds) ApplyToOutgoing(msg map[string]any) map[string]any {
	if w.isVSCode {
		w.guard.applyToOutgoing(msg)
	}
	dialect := w.dialect()
	if dialect == dialectCanonical {
		return msg
	}
	return rewriteFields(msg, func(uri string) string { return w.outbound(uri, dialect) }).(map[string]any)
}

// Active reports whether this connection needs any translation, so the host can skip decoding
// messages for the common canonical client.
func (w *ClientWorkarounds) Active() bool { return w.isVSCode || w.usesProv }

func (w *ClientWorkarounds) observeTraffic(method string, p map[string]any) {
	var listed []string
	for _, field := range []string{"initialSubscriptions", "subscriptions"} {
		if list, ok := p[field].([]any); ok {
			for _, item := range list {
				if s, ok := str(item); ok {
					listed = append(listed, s)
				}
			}
		}
	}
	observed := listed
	if direct, ok := str(p["channel"]); ok {
		observed = append([]string{direct}, listed...)
	}
	w.observeVSCode(p, observed)
	if w.usesProv {
		return
	}
	check := listed
	if providerSessionMethods[method] {
		check = observed
	}
	for _, uri := range check {
		if w.isProviderSession(uri) {
			w.usesProv = true
			return
		}
	}
}

func (w *ClientWorkarounds) observeVSCode(p map[string]any, observed []string) {
	if meta, ok := p["_meta"].(map[string]any); ok {
		_, a := meta["vscode.telemetryLevel"]
		_, b := meta["vscode.clientConnectionKind"]
		if a || b {
			w.isVSCode = true
			return
		}
	}
	for _, uri := range observed {
		if _, ok := w.sessionFromDerivedChat(uri); ok {
			w.isVSCode = true
			return
		}
	}
}

func (w *ClientWorkarounds) dialect() uriDialect {
	if w.isVSCode {
		return dialectVSCode
	}
	if w.usesProv {
		return dialectProvider
	}
	return dialectCanonical
}

// sessionFromDerivedChat translates a URI this client computed into the one this host minted: a
// derived chat URI is unwrapped regardless of who sent it, since it names no channel this host could
// otherwise serve.
func (w *ClientWorkarounds) sessionFromDerivedChat(uri string) (string, bool) {
	m := derivedChatURI.FindStringSubmatch(uri)
	if m == nil || m[1] == "" {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(m[1], "="))
	if err != nil {
		return "", false
	}
	session := string(raw)
	if !w.isProviderSession(session) {
		return "", false
	}
	return session, true
}

func (w *ClientWorkarounds) inbound(uri string, dialect uriDialect) string {
	if session, ok := w.sessionFromDerivedChat(uri); ok {
		if id, ok := w.providerSessionID(session); ok {
			return wire.ChatURI(id)
		}
	}
	if dialect == dialectCanonical || !w.isProviderSession(uri) {
		return uri
	}
	if id, ok := w.providerSessionID(uri); ok {
		return wire.SessionURI(id)
	}
	return uri
}

// outbound translates a URI this host minted into the dialect this client expects.
func (w *ClientWorkarounds) outbound(uri string, dialect uriDialect) string {
	if id, ok := wire.ChatIDFromURI(uri); ok {
		if dialect == dialectVSCode {
			return "ahp-chat://default/" + base64.RawURLEncoding.EncodeToString([]byte(w.provider+":/"+id))
		}
		return uri
	}
	if id, ok := w.providerSessionID(uri); ok {
		return w.provider + ":/" + id
	}
	return uri
}

// rewriteFields applies rewrite to every rewritable field, however deeply nested.
func rewriteFields(v any, rewrite func(string) string) any {
	switch x := v.(type) {
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = rewriteFields(item, rewrite)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, item := range x {
			if s, ok := item.(string); ok && rewritableFields[k] {
				out[k] = rewrite(s)
			} else {
				out[k] = rewriteFields(item, rewrite)
			}
		}
		return out
	}
	return v
}

// ── VS Code provisional-session disposal guard ──────────────────────────

type trackedState string

const (
	stateCreating     trackedState = "creating"
	stateEmpty        trackedState = "empty"
	stateMaterialized trackedState = "materialized"
	stateDisposing    trackedState = "disposing"
)

type pendingLifecycle struct {
	kind    string // "create" | "dispose"
	session string
}

// vscodeDisposalGuard allows VS Code to clean up only sessions known to be unused drafts on this
// connection. Unknown sessions are protected: reconnect does not carry enough history to prove that
// they are empty. Remove it when VS Code graduates materialized remote sessions before tearing down
// its provisional-session service.
type vscodeDisposalGuard struct {
	provider string
	sessions map[string]trackedState
	pending  map[string]pendingLifecycle
}

func newVSCodeDisposalGuard(provider string) *vscodeDisposalGuard {
	return &vscodeDisposalGuard{provider: provider, sessions: map[string]trackedState{}, pending: map[string]pendingLifecycle{}}
}

func idKey(v any) string {
	switch n := v.(type) {
	case json.Number:
		return n.String()
	case float64:
		b, _ := json.Marshal(n)
		return string(b)
	case string:
		return "s:" + n
	}
	return ""
}

func (g *vscodeDisposalGuard) canonical(uri string) (string, bool) {
	id, ok := wire.SessionIDFromURI(uri, g.provider)
	if !ok {
		return "", false
	}
	return wire.SessionURI(id), true
}

func (g *vscodeDisposalGuard) owning(uri string) (string, bool) {
	if s, ok := g.canonical(uri); ok {
		return s, true
	}
	if id, ok := wire.ChatIDFromURI(uri); ok {
		return wire.SessionURI(id), true
	}
	return "", false
}

func (g *vscodeDisposalGuard) markMaterialized(channel string) {
	if session, ok := g.owning(channel); ok {
		if _, tracked := g.sessions[session]; tracked {
			g.sessions[session] = stateMaterialized
		}
	}
}

func (g *vscodeDisposalGuard) applyToIncoming(msg map[string]any, p map[string]any) error {
	channel, _ := str(p["channel"])
	if t := actionTypeOf(p["action"]); channel != "" && t != "" && materializingActions[t] {
		g.markMaterialized(channel)
	}
	id, isRequest := msg["id"]
	if !isRequest || channel == "" {
		return nil
	}
	session, ok := g.canonical(channel)
	if !ok {
		return nil
	}
	method, _ := msg["method"].(string)
	switch method {
	case "createSession":
		if _, imported := p["importConversation"]; imported {
			g.sessions[session] = stateMaterialized
		} else {
			g.sessions[session] = stateCreating
		}
		g.pending[idKey(id)] = pendingLifecycle{"create", session}
	case "disposeSession":
		if g.sessions[session] != stateEmpty {
			return &wire.Error{Code: wire.CodeInvalidRequest, Message: vscodeMaterializedSessionDisposalRefusal}
		}
		g.sessions[session] = stateDisposing
		g.pending[idKey(id)] = pendingLifecycle{"dispose", session}
	}
	return nil
}

func (g *vscodeDisposalGuard) applyToOutgoing(msg map[string]any) {
	method, isNotification := msg["method"].(string)
	if !isNotification {
		g.applyResponse(msg)
		return
	}
	p := params(msg)
	if p == nil {
		return
	}
	if t := actionTypeOf(p["action"]); method == "action" && p["rejectionReason"] == nil && t != "" && materializingActions[t] {
		if channel, ok := str(p["channel"]); ok {
			g.markMaterialized(channel)
		}
	}
	if method == "root/sessionRemoved" {
		if s, ok := str(p["session"]); ok {
			if session, ok := g.canonical(s); ok {
				delete(g.sessions, session)
			}
		}
	}
}

func (g *vscodeDisposalGuard) applyResponse(msg map[string]any) {
	key := idKey(msg["id"])
	pending, ok := g.pending[key]
	if !ok {
		return
	}
	delete(g.pending, key)
	state := g.sessions[pending.session]
	_, success := msg["result"]
	if pending.kind == "create" {
		if success {
			if state == stateCreating {
				g.sessions[pending.session] = stateEmpty
			}
		} else {
			delete(g.sessions, pending.session)
		}
		return
	}
	if success {
		delete(g.sessions, pending.session)
	} else if state == stateDisposing {
		g.sessions[pending.session] = stateEmpty
	}
}
