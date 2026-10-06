//go:build unix

package egress

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// hitServer is an HTTP server that counts what reaches it.
type hitServer struct {
	*httptest.Server
	hits atomic.Int64
	mu   sync.Mutex
	reqs []string // method + " " + request URI
	host []string
}

func newHitServer(t *testing.T, handler http.HandlerFunc) *hitServer {
	t.Helper()
	h := &hitServer{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.hits.Add(1)
		h.mu.Lock()
		h.reqs = append(h.reqs, r.Method+" "+r.RequestURI)
		h.host = append(h.host, r.Host)
		h.mu.Unlock()
		if handler != nil {
			handler(w, r)
			return
		}
		_, _ = io.WriteString(w, "direct")
	}))
	t.Cleanup(h.Close)
	return h
}

func (h *hitServer) port() string { _, p, _ := net.SplitHostPort(h.Listener.Addr().String()); return p }
func (h *hitServer) addr() string { return h.Listener.Addr().String() }

func loopbackCIDR() []netip.Prefix { return []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")} }

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func clientFor(t *testing.T, c *profile.EgressPolicy, dns *fakeDNS) *http.Client {
	t.Helper()
	p := mustNew(t, c)
	if dns != nil {
		p = p.WithResolver(dns.resolver())
	}
	cl, err := p.Client(5 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return cl
}

func TestAnAllowedNameResolvingToAnOperatorRangeIsReached(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	resp, err := c.Get("http://allowed.test:" + srv.port() + "/x")
	if err != nil {
		t.Fatal(err)
	}
	if got := body(t, resp); got != "direct" || srv.hits.Load() != 1 {
		t.Fatalf("body=%q hits=%d", got, srv.hits.Load())
	}
}

func TestAnUnlistedHostIsRefusedBeforeAnyDial(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"unlisted.test": {"127.0.0.1"}, "allowed.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	for _, u := range []string{"unlisted.test", "evil-allowed.test", "allowed.test.evil.test", "127.0.0.1"} {
		_, err := c.Get("http://" + u + ":" + srv.port() + "/")
		if !profile.IsCode(err, profile.EgressDenied) {
			t.Errorf("%s: err = %v", u, err)
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("the server was reached")
	}
	if dns.lookups("unlisted.test") != 0 || dns.lookups("evil-allowed.test") != 0 {
		t.Fatal("a refused host was resolved")
	}
}

func TestAPublicNameThatResolvesToAPrivateAddressIsRefusedInTheDialler(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"pub.test": {"127.0.0.1"}, "mapped.test": {"::ffff:127.0.0.1"}, "nat64.test": {"64:ff9b::7f00:1"}, "meta.test": {"169.254.169.254"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"pub.test", "mapped.test", "nat64.test", "meta.test"}}, dns)
	for _, host := range []string{"pub.test", "mapped.test", "nat64.test", "meta.test"} {
		_, err := c.Get("http://" + host + ":" + srv.port() + "/")
		if !profile.IsCode(err, profile.EgressAddressDenied) {
			t.Errorf("%s: err = %v", host, err)
		}
	}
	if srv.hits.Load() != 0 {
		t.Fatal("the server was reached")
	}
}

func TestMetadataAddressesStayDeniedInsideTheOperatorRange(t *testing.T) {
	dns := newFakeDNS(map[string][]string{"meta.test": {"169.254.169.254"}, "meta6.test": {"fd00:ec2::254"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"meta.test", "meta6.test"},
		AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("fd00::/8")}}, dns)
	for _, host := range []string{"meta.test", "meta6.test"} {
		if _, err := c.Get("http://" + host + "/latest/meta-data/"); !profile.IsCode(err, profile.EgressAddressDenied) {
			t.Errorf("%s: err = %v", host, err)
		}
	}
}

func TestEveryAddressTheDiallerTriesIsChecked(t *testing.T) {
	l1, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l1.Close()
	_, port, _ := net.SplitHostPort(l1.Addr().String())
	l2, err := net.Listen("tcp", "127.0.0.2:"+port)
	if err != nil {
		t.Skip("127.0.0.2 is not available:", err)
	}
	defer l2.Close()
	var denied, allowed atomic.Int64
	serve := func(l net.Listener, n *atomic.Int64) {
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { n.Add(1); _, _ = io.WriteString(w, "ok") })}
		go srv.Serve(l)
	}
	serve(l1, &allowed)
	serve(l2, &denied)
	// One public-looking record that is private (127.0.0.2, outside the range) and one inside the range: only the
	// record inside the range may be connected to, whichever order the dialler tries them in.
	for _, order := range [][]string{{"127.0.0.2", "127.0.0.1"}, {"127.0.0.1", "127.0.0.2"}} {
		dns := newFakeDNS(map[string][]string{"mixed.test": order})
		c := clientFor(t, &profile.EgressPolicy{Allow: []string{"mixed.test"}, AllowCIDRs: loopbackCIDR()}, dns)
		resp, err := c.Get("http://mixed.test:" + port + "/")
		if err != nil {
			t.Fatalf("%v: %v", order, err)
		}
		resp.Body.Close()
	}
	if denied.Load() != 0 {
		t.Fatal("a connection was made to the address outside the range")
	}
	if allowed.Load() != 2 {
		t.Fatalf("allowed = %d", allowed.Load())
	}
	// All records denied: no connection at all.
	dns := newFakeDNS(map[string][]string{"allprivate.test": {"127.0.0.2", "10.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allprivate.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	if _, err := c.Get("http://allprivate.test:" + port + "/"); !profile.IsCode(err, profile.EgressAddressDenied) {
		t.Fatal(err)
	}
	if denied.Load() != 0 {
		t.Fatal("a connection was made to a denied address")
	}
}

func TestTheAddressDialledIsTheOneCheckedNotAnEarlierAnswer(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"rebind.test": {"127.0.0.1"}})
	p := mustNew(t, &profile.EgressPolicy{Allow: []string{"rebind.test"}, AllowCIDRs: loopbackCIDR()}).WithResolver(dns.resolver())
	c, _ := p.Client(5 * time.Second)
	// The static check does no DNS, so there is no earlier answer to go stale.
	before := dns.queries.Load()
	if err := p.Check("rebind.test"); err != nil {
		t.Fatal(err)
	}
	if dns.queries.Load() != before {
		t.Fatal("Check resolved the name")
	}
	// The name now resolves to an address outside the range: the dial is refused although Check passed.
	dns.set("rebind.test", "127.0.0.3")
	if _, err := c.Get("http://rebind.test:" + srv.port() + "/"); !profile.IsCode(err, profile.EgressAddressDenied) {
		t.Fatalf("err = %v", err)
	}
	if srv.hits.Load() != 0 {
		t.Fatal("reached")
	}
	// And back: the next dial is checked on its own answer.
	dns.set("rebind.test", "127.0.0.1")
	resp, err := c.Get("http://rebind.test:" + srv.port() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestRedirectsAreCheckedOnEveryHop(t *testing.T) {
	target := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}, "other.test": {"127.0.0.1"}, "private.test": {"127.0.0.2"}})
	for name, location := range map[string]string{
		"unlisted host":           "http://other.test:" + target.port() + "/landed",
		"private IP literal":      "http://127.0.0.2:" + target.port() + "/landed",
		"listed but private name": "http://private.test:" + target.port() + "/landed",
		"another scheme":          "ftp://allowed.test/landed",
		"metadata literal":        "http://169.254.169.254/latest/meta-data/",
	} {
		hop := newHitServer(t, func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, location, http.StatusFound) })
		c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test", "private.test", "127.0.0.2"}, AllowCIDRs: loopbackCIDR()}, dns)
		_, err := c.Get("http://allowed.test:" + hop.port() + "/start")
		code := profile.CodeOf(err)
		if code != profile.EgressDenied && code != profile.EgressAddressDenied {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if target.hits.Load() != 0 {
		t.Fatal("a redirect target was reached")
	}
}

func TestARedirectBetweenListedHostsIsFollowedAndALoopIsStopped(t *testing.T) {
	dst := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"a.test": {"127.0.0.1"}, "b.test": {"127.0.0.1"}})
	src := newHitServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://b.test:"+dst.port()+"/landed", http.StatusFound)
	})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"a.test", "b.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	resp, err := c.Get("http://a.test:" + src.port() + "/")
	if err != nil || body(t, resp) != "direct" || dst.hits.Load() != 1 {
		t.Fatalf("err=%v hits=%d", err, dst.hits.Load())
	}
	var loop *hitServer
	loop = newHitServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://a.test:"+loop.port()+"/again", http.StatusFound)
	})
	_, err = c.Get("http://a.test:" + loop.port() + "/")
	if !profile.IsCode(err, profile.EgressDenied) {
		t.Fatalf("loop: %v", err)
	}
	if n := loop.hits.Load(); n != MaxRedirects {
		t.Fatalf("hits = %d, want %d", n, MaxRedirects)
	}
}

func TestEnvironmentProxiesAreIgnored(t *testing.T) {
	envProxy := newHitServer(t, nil)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
		t.Setenv(k, envProxy.URL)
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	target := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	resp, err := c.Get("http://allowed.test:" + target.port() + "/")
	if err != nil || body(t, resp) != "direct" {
		t.Fatal(err)
	}
	if envProxy.hits.Load() != 0 {
		t.Fatal("the environment proxy was used")
	}
	// Also with a profile proxy: it, and only it, is used.
	profProxy := newHitServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "via-profile-proxy") })
	c = clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test", "127.0.0.1"}, AllowCIDRs: loopbackCIDR(), Proxy: "http://" + profProxy.addr()}, dns)
	resp, err = c.Get("http://allowed.test:" + target.port() + "/")
	if err != nil || body(t, resp) != "via-profile-proxy" {
		t.Fatal(err)
	}
	if envProxy.hits.Load() != 0 || target.hits.Load() != 1 {
		t.Fatalf("env proxy hits %d, target hits %d", envProxy.hits.Load(), target.hits.Load())
	}
}

func TestAProxyOnTheBaseTransportIsReplaced(t *testing.T) {
	chosen := newHitServer(t, nil) // a proxy the model chose
	target := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	p := mustNew(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}).WithResolver(dns.resolver())
	pu, _ := url.Parse(chosen.URL)
	base := &http.Transport{
		Proxy:                 http.ProxyURL(pu),
		DialContext:           (&net.Dialer{}).DialContext,
		Dial:                  func(string, string) (net.Conn, error) { return net.Dial("tcp", chosen.addr()) },
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
		ProxyConnectHeader:    http.Header{"X-Chosen": {"1"}},
		GetProxyConnectHeader: nil,
	}
	tr, err := p.Transport(base)
	if err != nil {
		t.Fatal(err)
	}
	if tr.TLSClientConfig.InsecureSkipVerify || tr.Dial != nil || tr.ProxyConnectHeader != nil {
		t.Fatalf("the base's settings were kept: %+v", tr)
	}
	if base.Proxy == nil || base.TLSClientConfig == nil || !base.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("the base transport was modified")
	}
	resp, err := (&http.Client{Transport: tr}).Get("http://allowed.test:" + target.port() + "/")
	if err != nil || body(t, resp) != "direct" {
		t.Fatal(err)
	}
	if chosen.hits.Load() != 0 {
		t.Fatal("the model's proxy was used")
	}
}

func TestTheBareTransportRefusesAnUnlistedHostToo(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"unlisted.test": {"127.0.0.1"}})
	p := mustNew(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}).WithResolver(dns.resolver())
	tr, err := p.Transport(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&http.Client{Transport: tr}).Get("http://unlisted.test:" + srv.port() + "/")
	if !profile.IsCode(err, profile.EgressDenied) || srv.hits.Load() != 0 || dns.lookups("unlisted.test") != 0 {
		t.Fatalf("err=%v hits=%d lookups=%d", err, srv.hits.Load(), dns.lookups("unlisted.test"))
	}
}

func TestAProxyIsDialledAndTheTargetIsStillChecked(t *testing.T) {
	proxy := newHitServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "via-proxy") })
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test", "10.0.0.5", "127.0.0.1"}, AllowCIDRs: loopbackCIDR(), Proxy: "http://" + proxy.addr()}, dns)

	resp, err := c.Get("http://allowed.test/x?q=1")
	if err != nil || body(t, resp) != "via-proxy" {
		t.Fatal(err)
	}
	proxy.mu.Lock()
	got := append([]string(nil), proxy.reqs...)
	proxy.mu.Unlock()
	if len(got) != 1 || got[0] != "GET http://allowed.test/x?q=1" {
		t.Fatalf("the proxy saw %q", got)
	}
	if dns.lookups("allowed.test") != 0 {
		t.Fatal("the target was resolved locally")
	}

	hits := proxy.hits.Load()
	// An unlisted target through the proxy: refused before the proxy is dialled.
	if _, err := c.Get("http://unlisted.test/"); !profile.IsCode(err, profile.EgressDenied) {
		t.Fatalf("unlisted: %v", err)
	}
	// A listed IP-literal target that is private and outside the range: refused locally too.
	if _, err := c.Get("http://10.0.0.5/"); !profile.IsCode(err, profile.EgressAddressDenied) {
		t.Fatalf("private literal: %v", err)
	}
	if proxy.hits.Load() != hits {
		t.Fatal("the proxy was contacted for a refused target")
	}
}

func TestTheProxyAddressIsCheckedLikeAnyOther(t *testing.T) {
	proxy := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	// 127.0.0.1 is not covered by the operator's range: the dial to the proxy is refused.
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test", "127.0.0.1"}, AllowCIDRs: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, Proxy: "http://" + proxy.addr()}, dns)
	if _, err := c.Get("http://allowed.test/"); !profile.IsCode(err, profile.EgressAddressDenied) {
		t.Fatalf("err = %v", err)
	}
	if proxy.hits.Load() != 0 {
		t.Fatal("the proxy was reached")
	}
}

func TestNoProxyTargetsAreDialledDirectlyWithTheFullCheck(t *testing.T) {
	proxy := newHitServer(t, func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "via-proxy") })
	direct := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"direct.test": {"127.0.0.1"}, "private.test": {"127.0.0.2"}, "proxied.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"direct.test", "private.test", "proxied.test", "127.0.0.1"}, AllowCIDRs: loopbackCIDR(),
		Proxy: "http://" + proxy.addr(), NoProxy: []string{"direct.test", ".private.test", "private.test"}}, dns)
	resp, err := c.Get("http://direct.test:" + direct.port() + "/")
	if err != nil || body(t, resp) != "direct" {
		t.Fatal(err)
	}
	if proxy.hits.Load() != 0 {
		t.Fatal("a NO_PROXY target went through the proxy")
	}
	if _, err := c.Get("http://private.test:" + direct.port() + "/"); !profile.IsCode(err, profile.EgressAddressDenied) {
		t.Fatalf("a direct target with a private address: %v", err)
	}
	resp, err = c.Get("http://proxied.test:" + direct.port() + "/")
	if err != nil || body(t, resp) != "via-proxy" {
		t.Fatalf("%v", err)
	}
}

// selfSigned returns a TLS configuration with a fresh self-signed certificate for 127.0.0.1 and the PEM of it.
func selfSigned(t *testing.T) (*tls.Config, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "egress test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func tlsServer(t *testing.T) (*httptest.Server, []byte) {
	cfg, caPEM := selfSigned(t)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "secure") }))
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv, caPEM
}

func TestHTTPSToATrustedAndAnUntrustedServer(t *testing.T) {
	srv, caPEM := tlsServer(t)
	_, otherPEM := selfSigned(t)
	dir := t.TempDir()
	bundle, other := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "other.pem")
	for p, data := range map[string][]byte{bundle: caPEM, other: otherPEM} {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	host, _, _ := net.SplitHostPort(srv.Listener.Addr().String())
	cfgFor := func(ca string) *profile.EgressPolicy {
		return &profile.EgressPolicy{Allow: []string{host}, AllowCIDRs: loopbackCIDR(), CABundle: ca}
	}
	resp, err := clientFor(t, cfgFor(bundle), nil).Get(srv.URL)
	if err != nil || body(t, resp) != "secure" {
		t.Fatalf("trusted bundle: %v", err)
	}
	// The system roots do not trust the test CA, and a bundle of another CA does not either: the named bundle
	// replaces the system roots.
	for name, ca := range map[string]string{"system roots": "", "another bundle": other} {
		if _, err := clientFor(t, cfgFor(ca), nil).Get(srv.URL); err == nil {
			t.Errorf("%s: an untrusted certificate was accepted", name)
		}
	}
	t.Setenv("SSL_CERT_FILE", bundle) // the system-roots variable is not an alternative to the named bundle
	if _, err := clientFor(t, cfgFor(other), nil).Get(srv.URL); err == nil {
		t.Error("SSL_CERT_FILE widened a named bundle")
	}
}

func TestInvalidCABundleRefusesToLoad(t *testing.T) {
	garbage := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(garbage, []byte("-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ca := range []string{garbage, filepath.Join(t.TempDir(), "missing.pem")} {
		_, err := New("websearch", &profile.EgressPolicy{Allow: []string{"a.example"}, CABundle: ca})
		if !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("%s: %v", ca, err)
		}
	}
}

func TestTransportIsSafeForConcurrentUse(t *testing.T) {
	srv := newHitServer(t, nil)
	dns := newFakeDNS(map[string][]string{"allowed.test": {"127.0.0.1"}})
	c := clientFor(t, &profile.EgressPolicy{Allow: []string{"allowed.test"}, AllowCIDRs: loopbackCIDR()}, dns)
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			host := "allowed.test"
			if i%2 == 1 {
				host = "evil" + strconv.Itoa(i) + ".test"
			}
			resp, err := c.Get(fmt.Sprintf("http://%s:%s/", host, srv.port()))
			if i%2 == 0 {
				if err != nil {
					errs <- err
					return
				}
				resp.Body.Close()
			} else if !profile.IsCode(err, profile.EgressDenied) {
				errs <- fmt.Errorf("%s: %v", host, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestClientAppliesTheTimeoutAndTheRoundTripperClosesRefusedBodies(t *testing.T) {
	p := mustNew(t, cfg("a.example"))
	c, err := p.Client(7 * time.Second)
	if err != nil || c.Timeout != 7*time.Second || c.CheckRedirect == nil {
		t.Fatalf("%+v %v", c, err)
	}
	rt, _ := p.RoundTripper(nil)
	body := &closeTracker{Reader: strings.NewReader("x")}
	req, _ := http.NewRequest(http.MethodPost, "http://evil.example/", body)
	if _, err := rt.RoundTrip(req); !profile.IsCode(err, profile.EgressDenied) || !body.closed {
		t.Fatalf("err=%v closed=%v", err, body.closed)
	}
	if p.CheckRedirect() == nil {
		t.Fatal("no redirect policy")
	}
}

type closeTracker struct {
	io.Reader
	closed bool
}

func (c *closeTracker) Close() error { c.closed = true; return nil }

func TestNoDialHappensAtConstruction(t *testing.T) {
	// Building a policy, a transport and a client resolves nothing and connects to nothing.
	dns := newFakeDNS(nil)
	p := mustNew(t, &profile.EgressPolicy{Allow: []string{"a.example", "proxy.example"}, Proxy: "http://proxy.example:3128"}).WithResolver(dns.resolver())
	if _, err := p.Transport(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Client(time.Second); err != nil {
		t.Fatal(err)
	}
	if dns.queries.Load() != 0 {
		t.Fatal("construction resolved a name")
	}
}
