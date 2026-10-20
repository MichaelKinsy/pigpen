package pi_permission_system

import (
	"github.com/MichaelKinsy/PiG/extensions/sdk"
)

// Port of src/authority/subagent-lifecycle-events.ts: subscribe to pi-subagents' child lifecycle events on the event bus and
// dispatch each fact to its owner: the registry for the two registration events, the audit for bound.
//
// The channel names and payload shapes are declared independently here and MUST match the publisher in @gotgenes/pi-subagents
// (src/lifecycle/child-lifecycle.ts). The session-created handler MUST stay synchronous: the core emits it on the same call stack
// immediately before bindExtensions(), and the bus finishes every listener before Emit returns (in PiG as well: sdk.EventBus.Emit),
// so a synchronous handler lands the registry entry before binding proceeds.

// Subagent child lifecycle channels.
const (
	// SubagentChildSessionCreated is emitted by the core after session creation, before bindExtensions().
	SubagentChildSessionCreated = "subagents:child:session-created"
	// SubagentChildBound is emitted by the core once the child's extensions have bound, after every child session_start handler has
	// run. Optional: an implementation that never emits it is still conformant, and simply forfeits the unguarded-child alarm.
	SubagentChildBound = "subagents:child:bound"
	// SubagentChildDisposed is emitted by the core in the run's finally (success and error).
	SubagentChildDisposed = "subagents:child:disposed"
)

// LifecycleEventBus is the minimal event-bus surface this module needs (subscribe only). The handler receives the emitter's JSON
// value; the returned function unsubscribes. A failure to register with the host is the error.
type LifecycleEventBus interface {
	On(channel string, handler func(data any)) (unsubscribe func(), err error)
}

// sdkLifecycleBus adapts the PiG SDK's event bus (the same bus pi-subagents emits on, shared across every extension process of the
// session) to LifecycleEventBus.
type sdkLifecycleBus struct{ bus sdk.EventBus }

// NewSDKLifecycleBus adapts `ctx.Events()` or `ext.Events()` to the lifecycle subscription.
func NewSDKLifecycleBus(bus sdk.EventBus) LifecycleEventBus { return sdkLifecycleBus{bus: bus} }

func (b sdkLifecycleBus) On(channel string, handler func(data any)) (func(), error) {
	return b.bus.On(channel, func(_ sdk.Context, data any) error {
		handler(data)
		return nil
	})
}

// eventString reads a string field off an event payload (a decoded JSON object).
func eventString(data any, key string) (string, bool) {
	switch d := data.(type) {
	case map[string]any:
		s, ok := d[key].(string)
		return s, ok
	case *jsObject:
		return d.str(key)
	}
	return "", false
}

// SubscribeSubagentLifecycle subscribes to the subagent child lifecycle. The returned function detaches every handler (call it during
// session_shutdown). If a subscription fails, the ones already made are detached and the error returned.
func SubscribeSubagentLifecycle(events LifecycleEventBus, registry *SubagentSessionRegistry, audit BoundChildAuditor) (func(), error) {
	var unsubs []func()
	detach := func() {
		for _, u := range unsubs {
			u()
		}
	}
	subscribe := func(channel string, handler func(data any)) error {
		u, err := events.On(channel, handler)
		if err != nil {
			detach()
			return err
		}
		unsubs = append(unsubs, u)
		return nil
	}

	if err := subscribe(SubagentChildSessionCreated, func(data any) {
		sessionID, ok := eventString(data, "sessionId")
		if !ok {
			return
		}
		var info SubagentSessionInfo
		if parent, ok := eventString(data, "parentSessionId"); ok {
			info.ParentSessionID = &parent
		}
		registry.Register(sessionID, info)
	}); err != nil {
		return nil, err
	}
	if err := subscribe(SubagentChildBound, func(data any) {
		sessionID, ok := eventString(data, "sessionId")
		if !ok {
			return
		}
		child := BoundChild{SessionID: sessionID}
		if parent, ok := eventString(data, "parentSessionId"); ok {
			child.ParentSessionID = &parent
		}
		audit.AuditBoundChild(child)
	}); err != nil {
		return nil, err
	}
	if err := subscribe(SubagentChildDisposed, func(data any) {
		if sessionID, ok := eventString(data, "sessionId"); ok {
			registry.Unregister(sessionID)
		}
	}); err != nil {
		return nil, err
	}
	return detach, nil
}
