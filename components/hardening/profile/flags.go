package profile

import (
	"net/netip"
	"time"
)

// Audit sinks.
const (
	SinkStderr      = "stderr"
	SinkSessionFile = "session-file"
)

// Defaults and bounds.
const (
	DefaultExpiryMargin = 30 * time.Second
	MaxExpiryMargin     = time.Hour
	DefaultLeeway       = 60 * time.Second
	MaxLeeway           = 300 * time.Second
	// MaxProfileBytes caps the profile file.
	MaxProfileBytes = 64 << 10
	// MaxCABundleBytes caps the CA bundle file.
	MaxCABundleBytes = 1 << 20
)

// CredentialFile configures the credentialFile flag.
type CredentialFile struct {
	// Path is the absolute path of the auth.json-shaped credential file.
	Path string
	// Provider is the key of the entry in that file.
	Provider string
	// Origin is "scheme://host[:port]", the only origin the credential is sent to. It is normalised.
	Origin string
	// Header is the request header that carries the credential (default Authorization).
	Header string
	// Scheme prefixes the value ("Bearer <token>"). The default is Bearer for Authorization and none otherwise.
	Scheme string
	// ExpiryMargin makes an OAuth token count as expired this long before it is (default 30 s).
	ExpiryMargin time.Duration
}

// EgressPolicy configures the egressPolicy flag.
type EgressPolicy struct {
	// Allow lists destination hosts: "host" matches exactly, ".suffix" matches names below it. Normalised.
	Allow []string
	// AllowCIDRs lists non-public ranges the operator exempts from the default address denial. Normalised.
	AllowCIDRs []netip.Prefix
	// Proxy is the only proxy used, "scheme://host:port" (http, https, socks5 or socks5h), or "".
	Proxy string
	// NoProxy lists hosts reached directly even when Proxy is set. Normalised, same syntax as Allow.
	NoProxy []string
	// CABundle is the absolute path of the PEM bundle that replaces the system roots, or "".
	CABundle string
}

// Audit configures the audit flag.
type Audit struct {
	// Sink is SinkStderr (the default) or SinkSessionFile.
	Sink string
	// Required makes a call fail when the sink cannot be written.
	Required bool
}

// ResourceServer configures the resourceServer flag.
type ResourceServer struct {
	Issuer     string
	Audience   string
	JWKSURL    string
	Algorithms []string
	Leeway     time.Duration
	// SubjectClaim and TenantClaim name the claims mapped to the principal (defaults sub and tenant).
	SubjectClaim string
	TenantClaim  string
}

// Flags is the result of Load. A nil pointer or a false bool means the flag is off; the zero value is the
// flag-off profile.
type Flags struct {
	CredentialFile *CredentialFile
	EgressPolicy   *EgressPolicy
	Audit          *Audit
	ResourceServer *ResourceServer
	// PolicyFailClosed is on. PolicyFile is the absolute Cedar policy file the profile names, or "".
	PolicyFailClosed bool
	PolicyFile       string
	Headless         bool

	pkg     string
	found   bool
	lastErr Code
}

// On reports whether the named flag is on.
func (f Flags) On(flag string) bool {
	switch flag {
	case FlagCredentialFile:
		return f.CredentialFile != nil
	case FlagEgressPolicy:
		return f.EgressPolicy != nil
	case FlagAudit:
		return f.Audit != nil
	case FlagResourceServer:
		return f.ResourceServer != nil
	case FlagPolicyFailClosed:
		return f.PolicyFailClosed
	case FlagHeadless:
		return f.Headless
	}
	return false
}

// Any reports whether any flag is on.
func (f Flags) Any() bool {
	for _, n := range FlagNames {
		if f.On(n) {
			return true
		}
	}
	return false
}

// Require returns ProfileMisconfigured when a flag that the Package does not support is on. A Package passes the
// flags of its row in the flag matrix. An unsupported flag that is on is an operator mistake, not a no-op.
func (f Flags) Require(supported ...string) error {
	ok := map[string]bool{}
	for _, s := range supported {
		ok[s] = true
	}
	for _, n := range FlagNames {
		if f.On(n) && !ok[n] {
			return NewError(ProfileMisconfigured, n, f.pkg)
		}
	}
	return nil
}

// CheckInsecureNoAuth returns ProfileMisconfigured when resourceServer is on together with an insecure no-auth
// setting of the Package (a2a's insecureNoAuth).
func (f Flags) CheckInsecureNoAuth(insecureNoAuth bool) error {
	if insecureNoAuth && f.ResourceServer != nil {
		return NewError(ProfileMisconfigured, FlagResourceServer, f.pkg)
	}
	return nil
}
