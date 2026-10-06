package resourceserver

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

const (
	testIssuer   = "https://idp.example/"
	testAudience = "a2a"
)

var (
	keysOnce                  sync.Once
	rsaKey, rsaKey2, rsaSmall *rsa.PrivateKey
	ecKey, ecKey384, ecKey521 *ecdsa.PrivateKey
	edKey                     ed25519.PrivateKey
)

func TestMain(m *testing.M) {
	must := func(e error) {
		if e != nil {
			panic(e)
		}
	}
	var err error
	rsaKey, err = rsa.GenerateKey(rand.Reader, 2048)
	must(err)
	rsaKey2, err = rsa.GenerateKey(rand.Reader, 2048)
	must(err)
	rsaSmall, err = rsa.GenerateKey(rand.Reader, 1024)
	must(err)
	ecKey, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	ecKey384, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	must(err)
	ecKey521, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
	must(err)
	_, edKey, err = ed25519.GenerateKey(rand.Reader)
	must(err)
	os.Exit(m.Run())
}

func pub(kid string, key any, mods ...func(*jose.JSONWebKey)) jose.JSONWebKey {
	k := jose.JSONWebKey{Key: key, KeyID: kid}
	for _, m := range mods {
		m(&k)
	}
	return k
}

// idp is a JWKS endpoint with a controllable key set and failure mode.
type idp struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	body      []byte
	status    int
	cache     string
	hits      atomic.Int64
	otherHits atomic.Int64 // requests to any path but /jwks
}

func newIDP(t *testing.T, keys ...jose.JSONWebKey) *idp {
	t.Helper()
	p := &idp{t: t, status: 200}
	p.setKeys(keys...)
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jwks" {
			p.otherHits.Add(1)
			http.NotFound(w, r)
			return
		}
		p.hits.Add(1)
		p.mu.Lock()
		body, status, cache := p.body, p.status, p.cache
		p.mu.Unlock()
		if cache != "" {
			w.Header().Set("Cache-Control", cache)
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *idp) setKeys(keys ...jose.JSONWebKey) {
	b, err := json.Marshal(jose.JSONWebKeySet{Keys: keys})
	if err != nil {
		p.t.Fatal(err)
	}
	p.mu.Lock()
	p.body = b
	p.mu.Unlock()
}

func (p *idp) setRaw(body string) { p.mu.Lock(); p.body = []byte(body); p.mu.Unlock() }
func (p *idp) setStatus(s int)    { p.mu.Lock(); p.status = s; p.mu.Unlock() }
func (p *idp) setCache(c string)  { p.mu.Lock(); p.cache = c; p.mu.Unlock() }
func (p *idp) url() string        { return p.srv.URL + "/jwks" }

// clock is a test clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

type env struct {
	t   *testing.T
	idp *idp
	clk *clock
	v   *Verifier
}

func newEnv(t *testing.T, keys ...jose.JSONWebKey) *env {
	t.Helper()
	e := &env{t: t, idp: newIDP(t, keys...), clk: newClock()}
	e.v = e.verifier(func(c *profile.ResourceServer) {})
	return e
}

func (e *env) config(mod func(*profile.ResourceServer)) *profile.ResourceServer {
	c := &profile.ResourceServer{Issuer: testIssuer, Audience: testAudience, JWKSURL: e.idp.url(), Algorithms: []string{"RS256", "ES256"},
		Leeway: 60 * time.Second, SubjectClaim: "sub", TenantClaim: "tenant"}
	mod(c)
	return c
}

func (e *env) verifier(mod func(*profile.ResourceServer)) *Verifier {
	v, err := New("a2a", e.config(mod), nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return v.WithClock(e.clk.Now)
}

// claims returns valid claims at the clock's time; keys in over replace them (a nil value removes the claim).
func (e *env) claims(over map[string]any) map[string]any {
	now := e.clk.Now()
	c := map[string]any{"iss": testIssuer, "aud": testAudience, "sub": "alice", "tenant": "acme",
		"exp": now.Add(time.Hour).Unix(), "nbf": now.Add(-time.Minute).Unix(), "iat": now.Add(-time.Minute).Unix()}
	for k, v := range over {
		if v == nil {
			delete(c, k)
		} else {
			c[k] = v
		}
	}
	return c
}

func sign(t *testing.T, alg jose.SignatureAlgorithm, key any, kid string, header map[string]any, claims map[string]any) string {
	t.Helper()
	opts := &jose.SignerOptions{ExtraHeaders: map[jose.HeaderKey]any{}}
	for k, v := range header {
		opts.ExtraHeaders[jose.HeaderKey(k)] = v
	}
	var sk jose.SigningKey
	if kid != "" {
		sk = jose.SigningKey{Algorithm: alg, Key: jose.JSONWebKey{Key: key, KeyID: kid}}
	} else {
		sk = jose.SigningKey{Algorithm: alg, Key: key}
	}
	s, err := jose.NewSigner(sk, opts)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(claims)
	jws, err := s.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := jws.CompactSerialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// craft builds a compact JWS with an arbitrary header, signing with signFn (nil: an empty signature).
func craft(t *testing.T, header map[string]any, claims map[string]any, signFn func(input []byte) []byte) string {
	t.Helper()
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	input := b64(h) + "." + b64(c)
	var sig []byte
	if signFn != nil {
		sig = signFn([]byte(input))
	}
	return input + "." + b64(sig)
}

func rs256(priv *rsa.PrivateKey) func([]byte) []byte {
	return func(in []byte) []byte {
		sum := sha256.Sum256(in)
		sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
		if err != nil {
			panic(err)
		}
		return sig
	}
}

func hs256(secret []byte) func([]byte) []byte {
	return func(in []byte) []byte {
		m := hmac.New(sha256.New, secret)
		m.Write(in)
		return m.Sum(nil)
	}
}

func publicKeyPEM(t *testing.T, k *rsa.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func wantCode(t *testing.T, err error, code profile.Code) {
	t.Helper()
	if !profile.IsCode(err, code) {
		t.Fatalf("err = %v, want %s", err, code)
	}
	if pe, ok := err.(*profile.Error); !ok || pe.Flag != profile.FlagResourceServer || pe.Package != "a2a" {
		t.Fatalf("flag/package not set: %#v", err)
	}
}
