// Package profile loads the opt-in enterprise profile of a Pigpen Package and defines the typed errors every
// hardening package returns.
//
// The profile is one JSON file, <agent dir>/pigpen-enterprise/<package>.json. With no file and no profile
// environment variable every flag is off and the library does nothing. A flag that is on and misconfigured fails
// closed with an *Error; it never degrades to the flag-off behaviour.
package profile

import (
	"context"
	"errors"
	"sort"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/ident"
)

// Code is a member of the closed set of failure codes.
type Code string

// The closed set. Each Code has one fixed, non-secret text.
const (
	ProfileMisconfigured  Code = "profile_misconfigured"
	CredentialUnavailable Code = "credential_unavailable"
	CredentialMalformed   Code = "credential_malformed"
	CredentialExpired     Code = "credential_expired"
	EgressDenied          Code = "egress_denied"
	EgressAddressDenied   Code = "egress_address_denied"
	EgressProxyIgnored    Code = "egress_proxy_ignored"
	AuditUnavailable      Code = "audit_unavailable"
	TokenInvalid          Code = "token_invalid"
	TokenAlgDenied        Code = "token_alg_denied"
	JWKSUnavailable       Code = "jwks_unavailable"
	PolicyUnavailable     Code = "policy_unavailable"
	PolicyError           Code = "policy_error"
	PolicyForbidden       Code = "policy_forbidden"
	// PolicyDenied is a deny with no matching permit (default deny). The plan's table has no code for it.
	PolicyDenied     Code = "policy_denied"
	JudgeUnavailable Code = "judge_unavailable"
	UIUnavailable    Code = "ui_unavailable"
	Cancelled        Code = "cancelled"
)

var texts = map[Code]string{
	ProfileMisconfigured:  "the enterprise profile is invalid or incomplete",
	CredentialUnavailable: "the credential file is missing, unreadable, not a regular file, or too large",
	CredentialMalformed:   "the credential file is malformed or has no usable entry for the provider",
	CredentialExpired:     "the credential has expired",
	EgressDenied:          "the destination is not on the egress allow-list",
	EgressAddressDenied:   "the destination address is not permitted",
	EgressProxyIgnored:    "a proxy named outside the profile was ignored",
	AuditUnavailable:      "the audit sink is unavailable",
	TokenInvalid:          "the access token is missing or invalid",
	TokenAlgDenied:        "the access token algorithm is not permitted",
	JWKSUnavailable:       "no usable signing key is available",
	PolicyUnavailable:     "the policy cannot be read or parsed",
	PolicyError:           "the policy could not be evaluated",
	PolicyForbidden:       "a policy forbids the call",
	PolicyDenied:          "no policy permits the call",
	JudgeUnavailable:      "the judge is unavailable",
	UIUnavailable:         "a dialog is needed and none can be shown",
	Cancelled:             "the operation was cancelled",
}

// Valid reports whether c is in the closed set.
func (c Code) Valid() bool { _, ok := texts[c]; return ok }

// Text returns the fixed text of c, or "" for a Code outside the set.
func (c Code) Text() string { return texts[c] }

// Codes returns the closed set in alphabetical order.
func Codes() []Code {
	out := make([]Code, 0, len(texts))
	for c := range texts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// The flag names. Flag is "" for an error that belongs to no single flag (the profile file itself).
const (
	FlagCredentialFile   = "credentialFile"
	FlagEgressPolicy     = "egressPolicy"
	FlagAudit            = "audit"
	FlagResourceServer   = "resourceServer"
	FlagPolicyFailClosed = "policyFailClosed"
	FlagHeadless         = "headless"
)

// FlagNames lists the closed set of flags in report order.
var FlagNames = []string{FlagCredentialFile, FlagEgressPolicy, FlagAudit, FlagResourceServer, FlagPolicyFailClosed, FlagHeadless}

func validFlag(f string) bool {
	for _, n := range FlagNames {
		if n == f {
			return true
		}
	}
	return false
}

// Error is the typed failure of every flag. Its text never contains a credential, token, claim, prompt, argument
// or path: Reason is fixed per Code and the other fields are validated identifiers.
type Error struct {
	Code    Code
	Flag    string
	Package string
	// Site names the place that would have opened a dialog (headless only). It is a validated identifier.
	Site   string
	Reason string

	cause error // only a context error; never printed
}

// NewError builds an Error. A Code outside the closed set becomes PolicyError (a deny), a Flag outside the closed
// set is dropped, and a Package or Site that is not an identifier is replaced by "invalid" (Package) or dropped (Site).
func NewError(code Code, flag, pkg string) *Error {
	if !code.Valid() {
		code = PolicyError
	}
	if !validFlag(flag) {
		flag = ""
	}
	if pkg != "" && !ident.Package(pkg) {
		pkg = "invalid"
	}
	return &Error{Code: code, Flag: flag, Package: pkg, Reason: texts[code]}
}

// WithSite returns a copy of e that names a dialog site. A site that is not an identifier is dropped.
func (e *Error) WithSite(site string) *Error {
	c := *e
	if ident.Package(site) {
		c.Site = site
	} else {
		c.Site = ""
	}
	return &c
}

// WithCause returns a copy of e that unwraps to a context error (and only to one).
func (e *Error) WithCause(err error) *Error {
	c := *e
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		c.cause = err
	}
	return &c
}

// Error returns "pigpen <package>: <flag>: <code>: <reason>", leaving out empty parts.
func (e *Error) Error() string {
	s := "pigpen"
	if e.Package != "" {
		s += " " + e.Package
	}
	s += ":"
	if e.Flag != "" {
		s += " " + e.Flag + ":"
	}
	s += " " + string(e.Code) + ": " + e.Reason
	if e.Site != "" {
		s += " (" + e.Site + ")"
	}
	return s
}

// Unwrap returns the context error of a cancelled operation, else nil.
func (e *Error) Unwrap() error { return e.cause }

// Is matches another *Error on Code, and on Flag, Package and Site when the target sets them. So
// errors.Is(err, &profile.Error{Code: profile.CredentialExpired}) tests the code alone.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || t == nil {
		return false
	}
	return t.Code == e.Code && (t.Flag == "" || t.Flag == e.Flag) && (t.Package == "" || t.Package == e.Package) && (t.Site == "" || t.Site == e.Site)
}

// CodeOf returns the Code of the first *Error in err's chain, or "" when there is none.
func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// IsCode reports whether err carries code.
func IsCode(err error, code Code) bool { return CodeOf(err) == code }
