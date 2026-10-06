// Package resourceserver admits a request only with a valid JWT access token (the OAuth 2.0 resource server side).
//
// The configuration names the issuer, the audience, the JWKS URL and the allowed algorithms. The JWKS URL is
// configured, never discovered from the token or from "iss". The signature is verified with go-jose against a key
// selected by "kid"; HS* and "none" are never accepted, so a public key cannot be used as an HMAC secret. The header
// fields jku, x5u, jwk and x5c are ignored, and a token with a crit header is rejected. A token, a claim or a key never
// appears in an error or a log line.
package resourceserver

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/MichaelKinsy/pigpen/components/hardening/internal/hostname"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/ident"
	"github.com/MichaelKinsy/pigpen/components/hardening/internal/strictjson"
	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// Limits.
const (
	// MaxTokenBytes caps a token (8 KiB).
	MaxTokenBytes = 8 << 10
	// MaxJWKSBytes caps a JWKS response (1 MiB).
	MaxJWKSBytes   = 1 << 20
	maxHeaderBytes = 2 << 10
	// MinCacheLifetime and MaxCacheLifetime bound the JWKS cache lifetime that Cache-Control max-age asks for.
	MinCacheLifetime = 5 * time.Minute
	MaxCacheLifetime = 24 * time.Hour
	// DefaultCacheLifetime is used when the response has no max-age.
	DefaultCacheLifetime = time.Hour
	// UnknownKIDRefreshInterval is the least time between two refreshes that an unknown kid triggers.
	UnknownKIDRefreshInterval = time.Minute
	// FailedRefreshBackoff is how long after a failed refresh the next request fails without trying again.
	FailedRefreshBackoff = 5 * time.Second
	fetchTimeout         = 10 * time.Second
	minRSABits           = 2048
)

// Principal is what the tokens subject and tenant claims map to. Both are validated identifiers.
type Principal struct {
	Subject string
	Tenant  string
}

// Verifier verifies tokens against one issuer, audience and JWKS.
type Verifier struct {
	pkg    string
	cfg    profile.ResourceServer
	algs   []jose.SignatureAlgorithm
	allow  map[string]bool
	client *http.Client
	now    func() time.Time

	sem chan struct{} // one JWKS fetch at a time

	mu          sync.Mutex // guards the fields below
	keys        []jose.JSONWebKey
	expires     time.Time
	lastAttempt time.Time
	lastFailed  bool
}

// New returns a Verifier for the resourceServer configuration of Package pkg. client fetches the JWKS: pass the
// egress policy's client when egressPolicy is on. A nil client is a plain client with a 10 second timeout that
// follows no redirect. New does no network work; the JWKS is fetched on the first token.
func New(pkg string, cfg *profile.ResourceServer, client *http.Client) (*Verifier, error) {
	bad := func() (*Verifier, error) {
		return nil, profile.NewError(profile.ProfileMisconfigured, profile.FlagResourceServer, pkg)
	}
	if cfg == nil || cfg.Issuer == "" || cfg.Audience == "" || cfg.JWKSURL == "" || len(cfg.Algorithms) == 0 ||
		cfg.Leeway < 0 || cfg.Leeway > profile.MaxLeeway || cfg.SubjectClaim == "" || cfg.TenantClaim == "" {
		return bad()
	}
	// The JWKS is fetched over https; only a loopback host may use http (the fixtures of a test host).
	if o, err := parseJWKSURL(cfg.JWKSURL); err != nil || (o.Scheme != "https" && !hostname.IsLoopback(o.Host)) {
		return bad()
	}
	v := &Verifier{pkg: pkg, cfg: *cfg, allow: map[string]bool{}, client: client, now: time.Now, sem: make(chan struct{}, 1)}
	known := map[string]bool{}
	for _, a := range profile.AllowedAlgorithms() {
		known[a] = true
	}
	for _, a := range cfg.Algorithms {
		if !known[a] {
			return bad()
		}
		v.allow[a] = true
		v.algs = append(v.algs, jose.SignatureAlgorithm(a))
	}
	if v.client == nil {
		v.client = &http.Client{Timeout: fetchTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return v, nil
}

// WithClock replaces the Verifier's clock and returns it (for tests; call it before the first token).
func (v *Verifier) WithClock(now func() time.Time) *Verifier { v.now = now; return v }

func parseJWKSURL(s string) (hostname.Origin, error) {
	u, err := url.Parse(s)
	if err != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return hostname.Origin{}, errFetch
	}
	return hostname.OriginOf(u)
}

func (v *Verifier) err(code profile.Code) *profile.Error {
	return profile.NewError(code, profile.FlagResourceServer, v.pkg)
}

// Verify checks a token and returns the principal. The errors are token_invalid, token_alg_denied, jwks_unavailable
// and cancelled; none contains any part of the token.
func (v *Verifier) Verify(ctx context.Context, token string) (Principal, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Principal{}, v.err(profile.Cancelled).WithCause(err)
	}
	header, jws, err := v.parse(token)
	if err != nil {
		return Principal{}, err
	}
	key, err := v.keyFor(ctx, header.kid, header.hasKID, header.alg)
	if err != nil {
		return Principal{}, err
	}
	payload, err := jws.Verify(key.Key)
	if err != nil {
		return Principal{}, v.err(profile.TokenInvalid)
	}
	return v.claims(payload)
}

type tokenHeader struct {
	alg    string
	kid    string
	hasKID bool
}

// parse checks the token's shape and protected header before any key is looked up.
func (v *Verifier) parse(token string) (tokenHeader, *jose.JSONWebSignature, error) {
	var h tokenHeader
	if token == "" || len(token) > MaxTokenBytes {
		return h, nil, v.err(profile.TokenInvalid)
	}
	// Compact serialization only: base64url segments and two dots. go-jose would strip whitespace and accept the
	// JSON serialization; neither is a JWT.
	dots := 0
	for i := 0; i < len(token); i++ {
		c := token[i]
		switch {
		case c == '.':
			dots++
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return h, nil, v.err(profile.TokenInvalid)
		}
	}
	if dots != 2 {
		return h, nil, v.err(profile.TokenInvalid)
	}
	seg := token[:strings.IndexByte(token, '.')]
	raw, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil || len(raw) == 0 || len(raw) > maxHeaderBytes {
		return h, nil, v.err(profile.TokenInvalid)
	}
	var fields map[string]json.RawMessage
	if strictjson.Decode(raw, &fields, false) != nil || fields == nil {
		return h, nil, v.err(profile.TokenInvalid)
	}
	var alg string
	if json.Unmarshal(fields["alg"], &alg) != nil || alg == "" || !v.allow[alg] {
		// Includes "none", HS256 and the other symmetric algorithms, a lower-case "rs256", and a missing alg.
		return h, nil, v.err(profile.TokenAlgDenied)
	}
	if _, has := fields["crit"]; has { // this verifier understands no critical header, and b64 (RFC 7797) is not a JWT
		return h, nil, v.err(profile.TokenInvalid)
	}
	if _, has := fields["b64"]; has {
		return h, nil, v.err(profile.TokenInvalid)
	}
	h.alg = alg
	if raw, has := fields["kid"]; has {
		if json.Unmarshal(raw, &h.kid) != nil || h.kid == "" || len(h.kid) > 256 {
			return h, nil, v.err(profile.TokenInvalid)
		}
		h.hasKID = true
	}
	jws, err := jose.ParseSignedCompact(token, v.algs)
	if err != nil || len(jws.Signatures) != 1 {
		return h, nil, v.err(profile.TokenInvalid)
	}
	return h, jws, nil
}

// keyFor returns the key to verify with, fetching the JWKS when the rules below say so:
//   - the cache is used while it is within its lifetime;
//   - a kid the cache does not have triggers one refresh, at most one a minute;
//   - an expired or empty cache triggers a refresh, and after a failed one the next request fails at once for 5 s;
//   - a failed refresh keeps the cached keys until their lifetime ends, but a token that needs a key the cache lacks
//     is rejected with jwks_unavailable.
func (v *Verifier) keyFor(ctx context.Context, kid string, hasKID bool, alg string) (jose.JSONWebKey, error) {
	select {
	case v.sem <- struct{}{}:
	case <-ctx.Done():
		return jose.JSONWebKey{}, v.err(profile.Cancelled).WithCause(ctx.Err())
	}
	held := true // refresh hands the semaphore to the fetch it starts
	defer func() {
		if held {
			<-v.sem
		}
	}()
	now := v.now()
	v.mu.Lock()
	fresh := len(v.keys) > 0 && now.Before(v.expires)
	key, found, ambiguous := v.lookup(kid, hasKID)
	sinceAttempt := now.Sub(v.lastAttempt)
	neverTried := v.lastAttempt.IsZero()
	failed := v.lastFailed
	v.mu.Unlock()

	switch {
	case fresh && ambiguous:
		return jose.JSONWebKey{}, v.err(profile.TokenInvalid)
	case fresh && found:
		return v.compatible(key, alg)
	case fresh && !hasKID:
		return jose.JSONWebKey{}, v.err(profile.TokenInvalid) // no kid, and not exactly one key
	case fresh && !neverTried && sinceAttempt < UnknownKIDRefreshInterval:
		return jose.JSONWebKey{}, v.err(profile.TokenInvalid) // an unknown kid, refreshed recently
	case !fresh && !neverTried && failed && sinceAttempt < FailedRefreshBackoff:
		return jose.JSONWebKey{}, v.err(profile.JWKSUnavailable)
	}
	held = false
	if err := v.refresh(ctx, now); err != nil {
		return jose.JSONWebKey{}, err
	}
	v.mu.Lock()
	key, found, ambiguous = v.lookup(kid, hasKID)
	v.mu.Unlock()
	if ambiguous || !found {
		return jose.JSONWebKey{}, v.err(profile.TokenInvalid)
	}
	return v.compatible(key, alg)
}

// lookup selects a key from the cache. With a kid it needs exactly one key with that id; without one the JWKS must
// have exactly one key. The caller holds v.mu.
func (v *Verifier) lookup(kid string, hasKID bool) (key jose.JSONWebKey, found, ambiguous bool) {
	if !hasKID {
		if len(v.keys) == 1 {
			return v.keys[0], true, false
		}
		return jose.JSONWebKey{}, false, false
	}
	n := 0
	for _, k := range v.keys {
		if k.KeyID == kid {
			key = k
			n++
		}
	}
	return key, n == 1, n > 1
}

// compatible checks that the key can verify the token's algorithm: the key type and curve, the key's use and alg.
func (v *Verifier) compatible(k jose.JSONWebKey, alg string) (jose.JSONWebKey, error) {
	if k.Use != "" && k.Use != "sig" {
		return jose.JSONWebKey{}, v.err(profile.TokenAlgDenied)
	}
	if k.Algorithm != "" && k.Algorithm != alg {
		return jose.JSONWebKey{}, v.err(profile.TokenAlgDenied)
	}
	if !keyFits(k.Key, alg) {
		return jose.JSONWebKey{}, v.err(profile.TokenAlgDenied)
	}
	return k, nil
}

func keyFits(key any, alg string) bool {
	switch k := key.(type) {
	case *rsa.PublicKey:
		return (strings.HasPrefix(alg, "RS") || strings.HasPrefix(alg, "PS")) && k.N.BitLen() >= minRSABits
	case *ecdsa.PublicKey:
		switch alg {
		case "ES256":
			return k.Curve == elliptic.P256()
		case "ES384":
			return k.Curve == elliptic.P384()
		case "ES512":
			return k.Curve == elliptic.P521()
		}
	case ed25519.PublicKey:
		return alg == "EdDSA"
	}
	return false
}

// refresh fetches the JWKS and replaces the cache. The caller holds the fetch semaphore, not v.mu; refresh takes it
// over and the fetch releases it when it ends.
//
// The fetch does not run on the caller's context. A token needs no valid signature to reach this point, so if a
// caller that abandons its request could cancel the fetch, and the failure were recorded, any client could keep every
// other caller on jwks_unavailable (by cancelling each first fetch) or spend the once-a-minute unknown-kid refresh on
// nothing. The fetch is bounded by its own timeout instead, and its result is cached whoever waits for it; the
// caller stops waiting when its context ends.
func (v *Verifier) refresh(ctx context.Context, now time.Time) error {
	done := make(chan error, 1)
	go func() {
		defer func() { <-v.sem }()
		var keys []jose.JSONWebKey
		var lifetime time.Duration
		err := errFetch
		func() {
			defer func() { _ = recover() }() // a panicking client is a failed fetch, not a crashed server
			keys, lifetime, err = v.fetch(context.WithoutCancel(ctx))
		}()
		v.mu.Lock()
		v.lastAttempt = now
		if err != nil {
			v.lastFailed = true
		} else {
			v.lastFailed = false
			v.keys = keys
			v.expires = now.Add(lifetime)
		}
		v.mu.Unlock()
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			return v.err(profile.JWKSUnavailable)
		}
		return nil
	case <-ctx.Done():
		return v.err(profile.Cancelled).WithCause(ctx.Err())
	}
}

var errFetch = errors.New("jwks fetch failed")

func (v *Verifier) fetch(ctx context.Context) ([]jose.JSONWebKey, time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, 0, errFetch
	}
	req.Header.Set("Accept", "application/json")
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, 0, errFetch
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, 0, errFetch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxJWKSBytes+1))
	if err != nil || len(body) > MaxJWKSBytes {
		return nil, 0, errFetch
	}
	// Each key is decoded on its own. go-jose refuses a whole key set when one member has a key type or curve it
	// does not support (an X25519 encryption key, a secp256k1 key), and an identity provider may publish such a key
	// next to its signing keys; that key is skipped, not the set.
	var set struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, 0, errFetch
	}
	var keys []jose.JSONWebKey
	for _, raw := range set.Keys {
		var k jose.JSONWebKey
		if err := k.UnmarshalJSON(raw); err != nil {
			continue
		}
		// Public signature keys only: a symmetric key, a private key or an invalid key is not a verification key.
		if k.Valid() && k.IsPublic() {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, 0, errFetch
	}
	return keys, cacheLifetime(resp.Header.Values("Cache-Control")), nil
}

// cacheLifetime reads max-age from Cache-Control and bounds it to [5 minutes, 24 hours]. No max-age gives an hour.
func cacheLifetime(values []string) time.Duration {
	lifetime := DefaultCacheLifetime
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(strings.ToLower(part))
			switch {
			case strings.HasPrefix(part, "max-age="):
				if n, err := strconv.ParseInt(strings.Trim(part[len("max-age="):], `"`), 10, 64); err == nil && n >= 0 {
					if n > int64(MaxCacheLifetime/time.Second) {
						n = int64(MaxCacheLifetime / time.Second)
					}
					lifetime = time.Duration(n) * time.Second
				}
			case part == "no-store" || part == "no-cache":
				lifetime = 0
			}
		}
	}
	if lifetime < MinCacheLifetime {
		lifetime = MinCacheLifetime
	}
	if lifetime > MaxCacheLifetime {
		lifetime = MaxCacheLifetime
	}
	return lifetime
}

// claims checks iss, aud, exp, nbf and iat, and maps the subject and tenant claims.
func (v *Verifier) claims(payload []byte) (Principal, error) {
	invalid := func() (Principal, error) { return Principal{}, v.err(profile.TokenInvalid) }
	var c map[string]json.RawMessage
	if strictjson.Decode(payload, &c, false) != nil || c == nil {
		return invalid()
	}
	var iss string
	if json.Unmarshal(c["iss"], &iss) != nil || iss != v.cfg.Issuer {
		return invalid()
	}
	if !audienceMatches(c["aud"], v.cfg.Audience) {
		return invalid()
	}
	now := v.now()
	exp, ok := numericDate(c["exp"])
	if !ok || !now.Before(exp.Add(v.cfg.Leeway)) { // exp is required
		return invalid()
	}
	if raw, present := c["nbf"]; present {
		nbf, ok := numericDate(raw)
		if !ok || now.Add(v.cfg.Leeway).Before(nbf) {
			return invalid()
		}
	}
	if raw, present := c["iat"]; present {
		iat, ok := numericDate(raw)
		if !ok || now.Add(v.cfg.Leeway).Before(iat) {
			return invalid()
		}
	}
	var p Principal
	if json.Unmarshal(c[v.cfg.SubjectClaim], &p.Subject) != nil || !ident.Tool(p.Subject) {
		return invalid()
	}
	if json.Unmarshal(c[v.cfg.TenantClaim], &p.Tenant) != nil || !ident.Tool(p.Tenant) {
		return invalid()
	}
	return p, nil
}

// audienceMatches is true when aud is the configured string, or an array that contains it. The match is exact.
func audienceMatches(raw json.RawMessage, want string) bool {
	if len(raw) == 0 {
		return false
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) != nil {
		return false
	}
	for _, a := range many {
		if a == want {
			return true
		}
	}
	return false
}

// numericDate reads a JWT NumericDate: a JSON number of seconds, possibly fractional.
func numericDate(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	var n json.Number
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&n) != nil || raw[0] == '"' {
		return time.Time{}, false
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 || f > 253402300799 { // year 9999
		return time.Time{}, false
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}
