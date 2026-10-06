package resourceserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func (e *env) verify(kid string, key any, alg jose.SignatureAlgorithm) error {
	e.t.Helper()
	_, err := e.v.Verify(context.Background(), sign(e.t, alg, key, kid, nil, e.claims(nil)))
	return err
}

func TestTheJWKSIsFetchedOnFirstUseAndCached(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	for i := 0; i < 5; i++ {
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
			t.Fatal(err)
		}
	}
	if e.idp.hits.Load() != 1 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
}

func TestCacheLifetimeFollowsMaxAgeWithinBounds(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		life   time.Duration
	}{
		"no header":         {"", time.Hour},
		"unrelated":         {"public", time.Hour},
		"max-age 600":       {"public, max-age=600", 10 * time.Minute},
		"max-age too small": {"max-age=60", MinCacheLifetime},
		"max-age zero":      {"max-age=0", MinCacheLifetime},
		"max-age too large": {"max-age=999999999", MaxCacheLifetime},
		"max-age a day":     {"max-age=86400", 24 * time.Hour},
		"no-store":          {"no-store", MinCacheLifetime},
		"no-cache":          {"no-cache", MinCacheLifetime},
		"garbage max-age":   {"max-age=soon", time.Hour},
		"negative max-age":  {"max-age=-5", time.Hour},
		"uppercase":         {"MAX-AGE=1200", 20 * time.Minute},
		"quoted":            {`max-age="1200"`, 20 * time.Minute},
	} {
		e := newEnv(t, pub("k1", &rsaKey.PublicKey))
		e.idp.setCache(tc.header)
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		e.clk.Add(tc.life - time.Second)
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil || e.idp.hits.Load() != 1 {
			t.Errorf("%s: inside the lifetime: %v, %d fetches", name, err, e.idp.hits.Load())
		}
		e.clk.Add(2 * time.Second)
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil || e.idp.hits.Load() != 2 {
			t.Errorf("%s: after the lifetime: %v, %d fetches", name, err, e.idp.hits.Load())
		}
	}
}

func TestAnUnknownKidTriggersOneRefreshAtMostOnceAMinute(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	// a rotated-in key shows up at the IdP, but the cache was filled a moment ago
	e.idp.setKeys(pub("k1", &rsaKey.PublicKey), pub("k2", &rsaKey2.PublicKey))
	e.clk.Add(10 * time.Second)
	wantCode(t, e.verify("k2", rsaKey2, jose.RS256), profile.TokenInvalid)
	if e.idp.hits.Load() != 1 {
		t.Fatalf("refreshed within a minute: %d fetches", e.idp.hits.Load())
	}
	// after a minute one unknown kid refreshes once and then the new key is found
	e.clk.Add(time.Minute)
	if err := e.verify("k2", rsaKey2, jose.RS256); err != nil {
		t.Fatal(err)
	}
	if e.idp.hits.Load() != 2 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
	// a kid that is not there: one refresh, then rejection, and no more for a minute
	e.clk.Add(2 * time.Minute)
	for i := 0; i < 20; i++ {
		wantCode(t, e.verify("nope", rsaKey, jose.RS256), profile.TokenInvalid)
	}
	if e.idp.hits.Load() != 3 {
		t.Fatalf("%d fetches after 20 unknown kids", e.idp.hits.Load())
	}
	e.clk.Add(61 * time.Second)
	wantCode(t, e.verify("nope", rsaKey, jose.RS256), profile.TokenInvalid)
	if e.idp.hits.Load() != 4 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
	// the known keys were never affected
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
}

func TestABurstOfUnknownKidsTriggersOneRefresh(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(2 * time.Minute)
	e.idp.setKeys(pub("k1", &rsaKey.PublicKey), pub("new", &rsaKey2.PublicKey))
	var wg sync.WaitGroup
	var rejected, accepted atomic.Int64
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 { // a legitimately rotated-in key
				if err := e.verify("new", rsaKey2, jose.RS256); err == nil {
					accepted.Add(1)
				}
			} else if e.verify("garbage", rsaKey, jose.RS256) != nil {
				rejected.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if e.idp.hits.Load() != 2 {
		t.Fatalf("%d fetches, want the first plus one refresh", e.idp.hits.Load())
	}
	if accepted.Load() != 20 || rejected.Load() != 20 {
		t.Fatalf("accepted %d, rejected %d", accepted.Load(), rejected.Load())
	}
}

func TestUnreachableJWKSWithNoCachedKey(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	e.idp.setStatus(http.StatusInternalServerError)
	wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
	// the next requests fail at once for five seconds, without hammering the IdP
	for i := 0; i < 10; i++ {
		wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
	}
	if e.idp.hits.Load() != 1 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
	// then it tries again, and recovers
	e.clk.Add(FailedRefreshBackoff + time.Second)
	e.idp.setStatus(http.StatusOK)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	if e.idp.hits.Load() != 2 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
}

func TestUnreachableJWKSWithACachedKeyInsideItsLifetime(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	e.idp.srv.Close() // truly unreachable
	e.clk.Add(30 * time.Minute)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatalf("a cached key inside its lifetime: %v", err)
	}
	// An unknown kid needs a refresh, which fails: the token is not accepted, and the known key still works.
	e.clk.Add(2 * time.Minute)
	wantCode(t, e.verify("new", rsaKey2, jose.RS256), profile.JWKSUnavailable)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatalf("a failed refresh dropped the cached key: %v", err)
	}
	// Past the lifetime nothing is usable.
	e.clk.Add(time.Hour)
	wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
}

func TestABadJWKSResponseKeepsTheCachedKeys(t *testing.T) {
	bad := map[string]func(*idp){
		"500":              func(p *idp) { p.setStatus(500) },
		"404":              func(p *idp) { p.setStatus(404) },
		"204":              func(p *idp) { p.setStatus(204) },
		"not json":         func(p *idp) { p.setRaw("<html>") },
		"empty":            func(p *idp) { p.setRaw("") },
		"no keys":          func(p *idp) { p.setRaw(`{"keys":[]}`) },
		"keys is a string": func(p *idp) { p.setRaw(`{"keys":"x"}`) },
		"only a symmetric key": func(p *idp) {
			p.setRaw(`{"keys":[{"kty":"oct","kid":"k1","k":"c2VjcmV0"}]}`)
		},
		"oversize": func(p *idp) { p.setRaw(`{"keys":[],"pad":"` + strings.Repeat("x", MaxJWKSBytes) + `"}`) },
	}
	for name, breakIt := range bad {
		e := newEnv(t, pub("k1", &rsaKey.PublicKey))
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
			t.Fatal(err)
		}
		breakIt(e.idp)
		e.clk.Add(2 * time.Minute)
		wantCode(t, e.verify("new", rsaKey2, jose.RS256), profile.JWKSUnavailable)
		if e.idp.hits.Load() != 2 {
			t.Errorf("%s: %d fetches", name, e.idp.hits.Load())
		}
		if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
			t.Errorf("%s: the cached key was lost: %v", name, err)
		}
	}
}

func TestOnlyPublicSignatureKeysAreUsed(t *testing.T) {
	e := newEnv(t)
	// A JWKS that (wrongly) publishes a private key and a symmetric key, next to a good public key.
	privJSON, _ := json.Marshal(jose.JSONWebKey{Key: rsaKey2, KeyID: "private"})
	goodJSON, _ := json.Marshal(pub("k1", &rsaKey.PublicKey))
	e.idp.setRaw(`{"keys":[` + string(privJSON) + `,{"kty":"oct","kid":"sym","k":"c2VjcmV0"},` + string(goodJSON) + `]}`)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	// the published private key is not a verification key
	e.clk.Add(2 * time.Minute)
	wantCode(t, e.verify("private", rsaKey2, jose.RS256), profile.TokenInvalid)
	// with only a symmetric key, an HS256 token is refused before and after
	tok := craft(t, map[string]any{"alg": "HS256", "kid": "sym"}, e.claims(nil), hs256([]byte("secret")))
	_, err := e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenAlgDenied)
}

// A key go-jose cannot decode (an X25519 encryption key, an unknown key type, a secp256k1 curve) is skipped; it does
// not make the identity provider's whole key set unusable. A set with nothing usable stays jwks_unavailable.
func TestAnUndecodableKeyIsSkippedNotTheSet(t *testing.T) {
	goodJSON, _ := json.Marshal(pub("k1", &rsaKey.PublicKey))
	odd := `{"kty":"OKP","crv":"X25519","kid":"enc","use":"enc","x":"hSDwCYkwp1R0i33ctD73Wg2_Og0mOBr066SpjqqbTmo"},` +
		`{"kty":"FUTURE","kid":"f"},{"kty":"EC","crv":"secp256k1","kid":"k","x":"AA","y":"AA"},"not a key",`
	e := newEnv(t)
	e.idp.setRaw(`{"keys":[` + odd + string(goodJSON) + `]}`)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatal(err)
	}
	e = newEnv(t)
	e.idp.setRaw(`{"keys":[` + strings.TrimSuffix(odd, ",") + `]}`)
	wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
}

func TestKeySelection(t *testing.T) {
	// no kid: accepted only when the JWKS has exactly one key
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	if err := e.verify("", rsaKey, jose.RS256); err != nil {
		t.Fatalf("one key, no kid: %v", err)
	}
	e = newEnv(t, pub("k1", &rsaKey.PublicKey), pub("k2", &rsaKey2.PublicKey))
	wantCode(t, e.verify("", rsaKey, jose.RS256), profile.TokenInvalid)
	if e.idp.hits.Load() != 1 {
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
	// a single key does not answer for another kid
	e = newEnv(t, pub("k1", &rsaKey.PublicKey))
	wantCode(t, e.verify("other", rsaKey, jose.RS256), profile.TokenInvalid)
	// two keys under one kid are ambiguous
	e = newEnv(t, pub("dup", &rsaKey.PublicKey), pub("dup", &rsaKey2.PublicKey))
	wantCode(t, e.verify("dup", rsaKey, jose.RS256), profile.TokenInvalid)
	wantCode(t, e.verify("dup", rsaKey2, jose.RS256), profile.TokenInvalid)
	// two keys, selected by kid; and EC next to RSA
	e = newEnv(t, pub("a", &rsaKey.PublicKey), pub("b", &rsaKey2.PublicKey), pub("c", &ecKey.PublicKey))
	for kid, k := range map[string]any{"a": rsaKey, "b": rsaKey2} {
		if err := e.verify(kid, k, jose.RS256); err != nil {
			t.Errorf("%s: %v", kid, err)
		}
	}
	if err := e.verify("c", ecKey, jose.ES256); err != nil {
		t.Error(err)
	}
	wantCode(t, e.verify("a", rsaKey2, jose.RS256), profile.TokenInvalid) // the right kid, another key's signature
	// the kid in the token is not a pointer into the key store: odd values are just unknown
	for _, kid := range []string{"../a", "a ", "A", "a\x00", strings.Repeat("a", 300)} {
		tok := craft(t, map[string]any{"alg": "RS256", "kid": kid}, e.claims(nil), rs256(rsaKey))
		if _, err := e.v.Verify(context.Background(), tok); !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("kid %q: %v", kid, err)
		}
	}
	tok := craft(t, map[string]any{"alg": "RS256", "kid": 7}, e.claims(nil), rs256(rsaKey))
	if _, err := e.v.Verify(context.Background(), tok); !profile.IsCode(err, profile.TokenInvalid) {
		t.Errorf("numeric kid: %v", err)
	}
}

func TestTheJWKSURLIsTheConfiguredOneAndRedirectsAreNotFollowed(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, e.idp.url(), http.StatusFound)
	}))
	defer redirecting.Close()
	v, err := New("a2a", e.config(func(c *profile.ResourceServer) { c.JWKSURL = redirecting.URL }), nil)
	if err != nil {
		t.Fatal(err)
	}
	v = v.WithClock(e.clk.Now)
	_, err = v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil)))
	wantCode(t, err, profile.JWKSUnavailable)
	if e.idp.hits.Load() != 0 {
		t.Fatal("a redirect from the JWKS URL was followed")
	}
	// a token's iss is not a JWKS location either
	other := newIDP(t, pub("k1", &rsaKey2.PublicKey))
	tok := sign(t, jose.RS256, rsaKey2, "k1", nil, e.claims(map[string]any{"iss": other.srv.URL}))
	_, err = e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenInvalid)
	if other.hits.Load() != 0 || other.otherHits.Load() != 0 {
		t.Fatal("the issuer claim was used as a location")
	}
}

type countingTransport struct {
	n    atomic.Int64
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(r)
}

func TestTheGivenClientFetchesTheJWKS(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	ct := &countingTransport{next: http.DefaultTransport}
	v, err := New("a2a", e.config(func(*profile.ResourceServer) {}), &http.Client{Transport: ct})
	if err != nil {
		t.Fatal(err)
	}
	v = v.WithClock(e.clk.Now)
	if _, err := v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))); err != nil {
		t.Fatal(err)
	}
	if ct.n.Load() != 1 {
		t.Fatalf("the given client made %d requests", ct.n.Load())
	}
	// and an egress refusal from that client is just an unavailable JWKS
	deny := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, profile.NewError(profile.EgressDenied, profile.FlagEgressPolicy, "a2a")
	})}
	v, _ = New("a2a", e.config(func(*profile.ResourceServer) {}), deny)
	_, err = v.WithClock(e.clk.Now).Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil)))
	wantCode(t, err, profile.JWKSUnavailable)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestACancelledFetchIsCancelledNotUnavailable(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	started := make(chan struct{})
	slow := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	v, _ := New("a2a", e.config(func(*profile.ResourceServer) {}), slow)
	v = v.WithClock(e.clk.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := v.Verify(ctx, sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil)))
		done <- err
	}()
	<-started
	cancel()
	wantCode(t, <-done, profile.Cancelled)
}

// A token needs no valid signature to start a JWKS fetch. A caller that abandons its request during that fetch must
// not cancel it, or record it as failed: otherwise anyone could keep every other caller on jwks_unavailable by
// cancelling each first fetch with a forged token. The fetch finishes, its keys are cached, and the next caller is
// admitted without another fetch.
func TestAnAbandonedRequestDoesNotCancelOrPoisonTheFetch(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	started, release := make(chan struct{}), make(chan struct{})
	var fetches atomic.Int64
	slow := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if fetches.Add(1) == 1 {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return nil, r.Context().Err()
			}
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	v, _ := New("a2a", e.config(func(*profile.ResourceServer) {}), slow)
	v = v.WithClock(e.clk.Now)

	forged := craft(t, map[string]any{"alg": "RS256", "kid": "k1"}, e.claims(nil), func([]byte) []byte { return []byte("forged") })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := v.Verify(ctx, forged); done <- err }()
	<-started
	cancel()
	wantCode(t, <-done, profile.Cancelled) // the abandoned caller stops waiting at once
	close(release)

	if _, err := v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))); err != nil {
		t.Fatalf("a valid token after an abandoned fetch: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Fatalf("%d fetches, want the abandoned one to have served the next caller", n)
	}
}

// A client whose transport panics fails the fetch; it does not crash the process from the fetch goroutine.
func TestAPanickingClientIsAFailedFetch(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	boom := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { panic("boom") })}
	v, _ := New("a2a", e.config(func(*profile.ResourceServer) {}), boom)
	_, err := v.WithClock(e.clk.Now).Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil)))
	wantCode(t, err, profile.JWKSUnavailable)
}
