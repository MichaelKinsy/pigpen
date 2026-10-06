// Package cedar is a policy.Decider backed by Cedar policies (github.com/cedar-policy/cedar-go).
//
// It is a nested module so that a Package that does not use Cedar does not carry cedar-go in its module graph. Only
// static policies are supported: no templates, no schema validation, and nothing from cedar-go's x/exp packages.
// Evaluation is deterministic and has no side effects. The mapping of a tool call to a Cedar request is fixed:
//
//	principal  Session::"<Principal>", with attributes subject and tenant when the request has them
//	action     Action::"<tool name>", a member of Action::"<group>" for each ActionGroups entry
//	resource   Target::"<normalised target>": the cleaned absolute path, the normalised URL host, or "none"
//	context    the tool arguments as a record (see Value)
//
// A call is allowed only when a permit matches, no forbid matches, and no policy reports an evaluation error. Cedar
// skips a policy whose evaluation errors, so an erroring forbid would not deny; this Decider therefore denies when
// any policy errors (policy_error).
package cedar

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	cedar "github.com/cedar-policy/cedar-go"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/hostname"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/ident"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/safefile"
	"github.com/MichaelKinsy/pigpen/components/hardening/policy"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// MaxPolicyBytes caps a policy file (1 MiB).
const MaxPolicyBytes = 1 << 20

// Limits on the request, so a hostile argument cannot make evaluation expensive.
const (
	maxIDBytes       = 1024
	maxResourceBytes = 8192
)

// Decider decides with a parsed policy set.
type Decider struct {
	set *cedar.PolicySet
}

var _ policy.Decider = (*Decider)(nil)

// Load reads and parses a Cedar policy file. On any failure (missing, unreadable, not a regular file, too large,
// not parseable) it returns a typed policy_unavailable error and a Decider that denies every request, never nil, so
// a Package that ignores the error still denies. A readable file that holds no policy is a Decider that denies
// every request with policy_denied.
func Load(path string) (policy.Decider, error) {
	data, err := safefile.Read(path, MaxPolicyBytes)
	if err != nil {
		return policy.Deny(profile.PolicyUnavailable), profile.NewError(profile.PolicyUnavailable, profile.FlagPolicyFailClosed, "")
	}
	return Parse(data)
}

// Parse parses Cedar policy text. See Load for failure behaviour.
func Parse(data []byte) (policy.Decider, error) {
	set, err := cedar.NewPolicySetFromBytes("policy.cedar", data)
	if err != nil || set == nil {
		return policy.Deny(profile.PolicyUnavailable), profile.NewError(profile.PolicyUnavailable, profile.FlagPolicyFailClosed, "")
	}
	return &Decider{set: set}, nil
}

func bad() error { return profile.NewError(profile.PolicyError, profile.FlagPolicyFailClosed, "") }

// Decide maps the request to Cedar and evaluates it. It returns policy_error for a request that cannot be mapped.
func (d *Decider) Decide(ctx context.Context, r policy.Request) (policy.Decision, error) {
	if d == nil || d.set == nil {
		return policy.Decision{}, profile.NewError(profile.PolicyUnavailable, profile.FlagPolicyFailClosed, "")
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return policy.Decision{}, profile.NewError(profile.Cancelled, profile.FlagPolicyFailClosed, "").WithCause(err)
		}
	}
	if r.Action == "" || len(r.Resource) > maxResourceBytes || len(r.Action) > maxIDBytes || len(r.Principal) > maxIDBytes || len(r.Subject) > maxIDBytes || len(r.Tenant) > maxIDBytes {
		return policy.Decision{}, bad()
	}
	req, entities, err := buildRequest(r)
	if err != nil {
		return policy.Decision{}, err
	}
	decision, diag := cedar.Authorize(d.set, entities, req)
	matched := make([]string, 0, len(diag.Reasons))
	for _, reason := range diag.Reasons {
		matched = append(matched, string(reason.PolicyID))
	}
	switch {
	case !bool(decision) && len(diag.Reasons) > 0:
		return policy.Decision{Reason: profile.PolicyForbidden, Matched: matched}, nil
	case len(diag.Errors) > 0:
		// A permit may have matched; a policy that errored may have been a forbid. Deny.
		return policy.Decision{Reason: profile.PolicyError}, nil
	case !bool(decision):
		return policy.Decision{Reason: profile.PolicyDenied}, nil
	}
	return policy.Decision{Allow: true, Matched: matched}, nil
}

func buildRequest(r policy.Request) (cedar.Request, cedar.EntityMap, error) {
	ctxRecord, err := Record(r.Context)
	if err != nil {
		return cedar.Request{}, nil, err
	}
	principal := cedar.NewEntityUID("Session", cedar.String(r.Principal))
	action := cedar.NewEntityUID("Action", cedar.String(r.Action))
	target, err := Target(r.Resource)
	if err != nil {
		return cedar.Request{}, nil, err
	}
	resource := cedar.NewEntityUID("Target", cedar.String(target))

	attrs := cedar.RecordMap{}
	if r.Subject != "" {
		attrs["subject"] = cedar.String(r.Subject)
	}
	if r.Tenant != "" {
		attrs["tenant"] = cedar.String(r.Tenant)
	}
	var groups []cedar.EntityUID
	for _, g := range r.ActionGroups {
		if !ident.Tool(g) {
			return cedar.Request{}, nil, bad()
		}
		groups = append(groups, cedar.NewEntityUID("Action", cedar.String(g)))
	}
	entities := cedar.EntityMap{
		principal: {UID: principal, Attributes: cedar.NewRecord(attrs)},
		action:    {UID: action, Parents: cedar.NewEntityUIDSet(groups...)},
	}
	return cedar.Request{Principal: principal, Action: action, Resource: resource, Context: ctxRecord}, entities, nil
}

// Target normalises a resource: "" is "none"; a URL is its host, normalised as the egress allow-list normalises it
// (lower case, no trailing dot, IDNA ASCII, an IPv4-mapped address unmapped), so a rule on Target::"example.com" also
// matches "https://EXAMPLE.com./" and the full-width or ideographic-dot spellings that reach the same host; anything
// else is an absolute path, cleaned (cleaned, not resolved through symbolic links: a path rule is not a sandbox).
// A URL without a host, a host that cannot be normalised, or a relative path cannot be matched reliably, so it is
// policy_error rather than a target a forbid might miss ("../../etc/shadow" would not equal Target::"/etc/shadow").
func Target(resource string) (string, error) {
	if resource == "" {
		return "none", nil
	}
	if strings.Contains(resource, "://") {
		u, err := url.Parse(resource)
		if err != nil || u.Hostname() == "" {
			return "", bad()
		}
		host, err := hostname.Normalize(u.Hostname())
		if err != nil {
			return "", bad()
		}
		return host, nil
	}
	if !filepath.IsAbs(resource) {
		return "", bad()
	}
	return filepath.Clean(resource), nil
}
