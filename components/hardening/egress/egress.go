// Package egress restricts a Package's outbound HTTP to the destinations an operator lists.
//
// The host name of every request, and of every redirect, is matched against an allow-list before anything is
// dialled. Every address the dialler tries is checked inside net.Dialer.ControlContext, on the address actually
// dialled, so there is no separate resolve step whose answer could change before the connection is made. Only the
// proxy and CA bundle the profile names are used: HTTP_PROXY and the other proxy variables, a per-request proxy,
// and the system roots (when a bundle is named) are ignored.
//
// With a proxy the dialler connects only to the proxy, which must itself be on the allow-list; the proxy resolves
// the target. The target host is still checked against the allow-list on every request and redirect, and an
// IP-literal target is checked against the address rules. The proxy is responsible for address policy on the far
// side. Hosts in NoProxy are dialled directly and get the full check.
package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/hostname"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/netrange"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/safefile"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// MaxRedirects bounds a redirect chain.
const MaxRedirects = 10

// Policy is a configured egress policy. The zero Policy is unusable: every method returns ProfileMisconfigured.
type Policy struct {
	pkg      string
	allow    []string
	cidrs    []netip.Prefix
	proxy    *url.URL
	noProxy  []string
	roots    *x509.CertPool
	resolver *net.Resolver
	ok       bool
}

// New builds a Policy from the egressPolicy configuration of Package pkg. It reads the CA bundle (a file read, no
// network). A nil configuration, an invalid entry, or an unreadable or invalid bundle is ProfileMisconfigured.
func New(pkg string, cfg *profile.EgressPolicy) (Policy, error) {
	bad := func() (Policy, error) {
		return Policy{}, profile.NewError(profile.ProfileMisconfigured, profile.FlagEgressPolicy, pkg)
	}
	if cfg == nil || len(cfg.Allow) == 0 {
		return bad()
	}
	p := Policy{pkg: pkg}
	for _, a := range cfg.Allow {
		n, err := hostname.ParseEntry(a)
		if err != nil || n != a {
			return bad()
		}
		p.allow = append(p.allow, n)
	}
	for _, c := range cfg.AllowCIDRs {
		n, err := netrange.NormalizePrefix(c)
		if err != nil || n != c {
			return bad()
		}
		p.cidrs = append(p.cidrs, n)
	}
	for _, e := range cfg.NoProxy {
		n, err := hostname.ParseEntry(e)
		if err != nil || n != e {
			return bad()
		}
		p.noProxy = append(p.noProxy, n)
	}
	if cfg.Proxy != "" {
		u, err := url.Parse(cfg.Proxy)
		if err != nil || u.Hostname() == "" || u.Port() == "" || u.User != nil {
			return bad()
		}
		switch u.Scheme {
		case "http", "https", "socks5", "socks5h":
		default:
			return bad()
		}
		// The proxy is a destination like any other: it must be on the allow-list.
		if h, err := hostname.Normalize(u.Hostname()); err != nil || !p.listed(h) {
			return bad()
		}
		p.proxy = u
	}
	if cfg.CABundle != "" {
		data, err := safefile.Read(cfg.CABundle, profile.MaxCABundleBytes)
		if err != nil {
			return bad()
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(data) {
			return bad()
		}
		p.roots = pool
	}
	p.ok = true
	return p, nil
}

// WithResolver returns a copy of p whose dialler resolves names with r instead of the default resolver.
func (p Policy) WithResolver(r *net.Resolver) Policy { p.resolver = r; return p }

func (p Policy) err(code profile.Code) *profile.Error {
	return profile.NewError(code, profile.FlagEgressPolicy, p.pkg)
}

// Check is the static check: the host must be on the allow-list, and an IP-literal host must also pass the address
// rules. It does no DNS. The host is lower-cased, stripped of a trailing dot and converted to IDNA ASCII first.
func (p Policy) Check(host string) error {
	if !p.ok {
		return profile.NewError(profile.ProfileMisconfigured, profile.FlagEgressPolicy, p.pkg)
	}
	h, err := hostname.Normalize(host)
	if err != nil {
		return p.err(profile.EgressDenied)
	}
	if !p.listed(h) {
		return p.err(profile.EgressDenied)
	}
	if a, err := netip.ParseAddr(h); err == nil {
		if netrange.Check(a, p.cidrs) != nil {
			return p.err(profile.EgressAddressDenied)
		}
	}
	return nil
}

// listed reports whether a normalised host matches an allow-list entry.
func (p Policy) listed(h string) bool {
	for _, e := range p.allow {
		if hostname.Match(e, h) {
			return true
		}
	}
	return false
}

// CheckURL checks a request URL: http or https, with a host that passes Check.
func (p Policy) CheckURL(u *url.URL) error {
	if !p.ok {
		return profile.NewError(profile.ProfileMisconfigured, profile.FlagEgressPolicy, p.pkg)
	}
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return p.err(profile.EgressDenied)
	}
	return p.Check(u.Hostname())
}

// control runs inside net.Dialer.ControlContext, once for each address the dialler is about to connect to.
func (p Policy) control(_ context.Context, network, address string, _ syscall.RawConn) error {
	if network != "tcp4" && network != "tcp6" && network != "tcp" {
		return p.err(profile.EgressAddressDenied)
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil || netrange.Check(ap.Addr(), p.cidrs) != nil {
		return p.err(profile.EgressAddressDenied)
	}
	return nil
}

func (p Policy) viaProxy(host string) bool {
	if p.proxy == nil {
		return false
	}
	h, err := hostname.Normalize(host)
	if err != nil {
		return true
	}
	for _, e := range p.noProxy {
		if hostname.Match(e, h) {
			return false
		}
	}
	return true
}

// Transport returns a clone of base (http.DefaultTransport's settings when base is nil) that checks the allow-list
// on every request, uses only the profile's proxy and CA bundle, and rejects denied addresses in the dialler's
// control hook. Settings of base that would open another path are replaced: its proxy function, dial functions,
// proxy-connect headers and TLS configuration.
func (p Policy) Transport(base *http.Transport) (*http.Transport, error) {
	if !p.ok {
		return nil, profile.NewError(profile.ProfileMisconfigured, profile.FlagEgressPolicy, p.pkg)
	}
	var t *http.Transport
	if base != nil {
		t = base.Clone()
	} else {
		t = http.DefaultTransport.(*http.Transport).Clone()
	}
	dialer := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second, Resolver: p.resolver, ControlContext: p.control}
	t.DialContext = dialer.DialContext
	t.Dial, t.DialTLS, t.DialTLSContext = nil, nil, nil //nolint:staticcheck // cleared on purpose: they would bypass the control hook
	t.Proxy = func(req *http.Request) (*url.URL, error) {
		// Called for every request, including a redirect, before any connection is made.
		if err := p.CheckURL(req.URL); err != nil {
			return nil, err
		}
		if p.viaProxy(req.URL.Hostname()) {
			return p.proxy, nil
		}
		return nil, nil
	}
	t.ProxyConnectHeader, t.GetProxyConnectHeader, t.OnProxyConnectResponse = nil, nil, nil
	t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: p.roots}
	t.ForceAttemptHTTP2 = true
	return t, nil
}

// RoundTripper returns Transport(base) behind a check of the allow-list that also closes the request body when it
// refuses a request.
func (p Policy) RoundTripper(base *http.Transport) (http.RoundTripper, error) {
	t, err := p.Transport(base)
	if err != nil {
		return nil, err
	}
	return checkTransport{p: p, next: t}, nil
}

type checkTransport struct {
	p    Policy
	next http.RoundTripper
}

func (c checkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := c.p.CheckURL(req.URL); err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return c.next.RoundTrip(req)
}

// Client returns an http.Client on RoundTripper(nil) that re-checks each redirect and stops after MaxRedirects.
func (p Policy) Client(timeout time.Duration) (*http.Client, error) {
	rt, err := p.RoundTripper(nil)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: rt, Timeout: timeout, CheckRedirect: p.checkRedirect}, nil
}

func (p Policy) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= MaxRedirects {
		return p.err(profile.EgressDenied)
	}
	return p.CheckURL(req.URL)
}

// CheckRedirect is the redirect policy of Client, for a caller that builds its own http.Client.
func (p Policy) CheckRedirect() func(*http.Request, []*http.Request) error { return p.checkRedirect }

// Denied reports whether err is an egress denial (a host or an address).
func Denied(err error) bool {
	c := profile.CodeOf(err)
	return c == profile.EgressDenied || c == profile.EgressAddressDenied
}
