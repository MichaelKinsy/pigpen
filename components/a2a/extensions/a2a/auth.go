package a2aext

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Principal is an authenticated caller.
type Principal struct {
	Name   string
	Tenant string
}

// Key is the isolation key for tasks and sessions: every token of one tenant shares
// it, a token without a tenant is its own boundary, and the two namespaces cannot collide.
func (p Principal) Key() string {
	if p.Tenant != "" {
		return "tenant:" + p.Tenant
	}
	return "token:" + p.Name
}

type tokenEntry struct {
	digest    [sha256.Size]byte
	principal Principal
}

// Authenticator maps bearer tokens to principals.
type Authenticator struct {
	entries  []tokenEntry
	insecure bool
}

// NewAuthenticator builds an Authenticator. Tokens are read from the environment variables
// the configuration names; only their SHA-256 digests are kept.
func NewAuthenticator(tokens []TokenConfig, getenv func(string) string, insecureNoAuth bool) (*Authenticator, error) {
	if insecureNoAuth {
		return &Authenticator{insecure: true}, nil
	}
	if len(tokens) == 0 {
		return nil, errors.New("a2a: no tokens configured; refusing to build an authenticator that accepts everyone or no one")
	}
	a := &Authenticator{}
	for _, t := range tokens {
		v := getenv(t.TokenEnv)
		if v == "" {
			return nil, fmt.Errorf("a2a: token %q: environment variable %s is not set", t.Name, t.TokenEnv)
		}
		a.entries = append(a.entries, tokenEntry{digest: sha256.Sum256([]byte(v)), principal: Principal{Name: t.Name, Tenant: t.Tenant}})
	}
	return a, nil
}

// Authenticate returns the caller's principal from the Authorization header. Tokens in
// URLs are never read: they end up in logs and proxies.
func (a *Authenticator) Authenticate(r *http.Request) (Principal, bool) {
	if a.insecure {
		return Principal{Name: "anonymous"}, true
	}
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "bearer") {
		return Principal{}, false
	}
	digest := sha256.Sum256([]byte(fields[1]))
	var found Principal
	ok := false
	for _, e := range a.entries { // no early exit: the time does not depend on which token matched
		if subtle.ConstantTimeCompare(digest[:], e.digest[:]) == 1 {
			found, ok = e.principal, true
		}
	}
	return found, ok
}

// loopbackHostHeader reports whether a Host header names a loopback address (localhost, 127.0.0.0/8, ::1).
func loopbackHostHeader(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.TrimSuffix(strings.TrimPrefix(hostport, "["), "]")
	}
	return isLoopbackHost(host)
}

type principalCtxKey struct{}

// PrincipalFrom returns the principal Wrap attached to ctx.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(Principal)
	return p, ok
}

// Wrap rejects unauthenticated requests with 401 and a Bearer challenge, except those
// isPublic accepts (the agent card), and passes the principal down in the request context.
func (a *Authenticator) Wrap(next http.Handler, isPublic func(*http.Request) bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.insecure && !loopbackHostHeader(r.Host) {
			// Without a credential, the Host header is what tells a local client from a web page that rebound
			// its own name to 127.0.0.1 (DNS rebinding); such a page would be same-origin with the listener.
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if isPublic != nil && isPublic(r) {
			next.ServeHTTP(w, r)
			return
		}
		p, ok := a.Authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalCtxKey{}, p)))
	})
}
