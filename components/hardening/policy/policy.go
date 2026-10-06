// Package policy defines the one contract a Package sees for a tool-call decision: a Decider. An engine (Cedar in
// the nested policy/cedar module; another can follow) implements it, and FailClosed makes any failure of it a deny.
package policy

import (
	"context"
	"reflect"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// MaxMatched bounds Decision.Matched.
const MaxMatched = 16

// Request is one tool call to decide.
type Request struct {
	// Principal is the session or caller identity.
	Principal string
	// Action is the tool name.
	Action string
	// Resource is what the tool acts on, if known: a file path, a URL, or "" for none.
	Resource string
	// Context holds the tool arguments. The engine, not the audit emitter, sees them.
	Context map[string]any
	// Subject and Tenant are the claims resourceServer maps to the principal, when it is on.
	Subject string
	Tenant  string
	// ActionGroups names the tool classes the action belongs to ("Shell", "FileWrite", "Network").
	ActionGroups []string
}

// Decision is the outcome. Allow is false unless a permit matched and nothing forbade or failed.
type Decision struct {
	Allow bool
	// Reason is a profile.Code for a deny (policy_denied, policy_forbidden, policy_error, policy_unavailable,
	// cancelled), and "" for an allow.
	Reason profile.Code
	// Matched lists the identifiers of the policies that decided, at most MaxMatched.
	Matched []string
}

// Decider decides a request. An error means "no decision": the caller must deny.
type Decider interface {
	Decide(ctx context.Context, r Request) (Decision, error)
}

func deny(code profile.Code) Decision { return Decision{Allow: false, Reason: code} }

// Deny returns a Decider that denies every request with code (policy_unavailable when the policy cannot be read).
func Deny(code profile.Code) Decider {
	if !code.Valid() {
		code = profile.PolicyError
	}
	return denyAll{code}
}

type denyAll struct{ code profile.Code }

func (d denyAll) Decide(context.Context, Request) (Decision, error) { return deny(d.code), nil }

// FailClosed wraps d so that any error, panic, nil decider, or finished context becomes a deny. The returned
// Decider never returns an error and never allows what d did not allow. Deny wins over allow: an allow that comes
// with an error, or with a reason (an allow has none), is a deny.
func FailClosed(d Decider) Decider { return failClosed{d} }

type failClosed struct{ d Decider }

func isNil(d Decider) bool {
	if d == nil {
		return true
	}
	v := reflect.ValueOf(d)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Interface, reflect.Chan:
		return v.IsNil()
	}
	return false
}

func (f failClosed) Decide(ctx context.Context, r Request) (out Decision, _ error) {
	defer func() {
		if recover() != nil { // the value of the panic is dropped: it may hold arguments
			out = deny(profile.PolicyError)
		}
	}()
	if isNil(f.d) {
		return deny(profile.PolicyUnavailable), nil
	}
	if ctx != nil && ctx.Err() != nil {
		return deny(profile.Cancelled), nil
	}
	dec, err := f.d.Decide(ctx, r)
	if err != nil {
		switch c := profile.CodeOf(err); c {
		case profile.PolicyUnavailable, profile.PolicyError, profile.Cancelled:
			return deny(c), nil
		}
		return deny(profile.PolicyError), nil
	}
	if !dec.Allow {
		if !dec.Reason.Valid() {
			dec.Reason = profile.PolicyDenied
		}
		dec.Matched = clip(dec.Matched)
		return dec, nil
	}
	if dec.Reason != "" { // an allow has no reason; one that has is a confused decider, and deny wins over allow
		return deny(profile.PolicyError), nil
	}
	return Decision{Allow: true, Matched: clip(dec.Matched)}, nil
}

func clip(m []string) []string {
	if len(m) == 0 {
		return nil
	}
	if len(m) > MaxMatched {
		m = m[:MaxMatched]
	}
	return append([]string(nil), m...)
}
