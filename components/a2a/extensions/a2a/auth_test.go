package a2aext

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

const (
	tokenA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	tokenB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func testAuth(t *testing.T) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator([]TokenConfig{
		{Name: "alice", TokenEnv: "TOKEN_A", Tenant: "team-a"},
		{Name: "bob", TokenEnv: "TOKEN_B"},
	}, envFrom(map[string]string{"TOKEN_A": tokenA, "TOKEN_B": tokenB}), false)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func request(header string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://x/", nil)
	if header != "" {
		r.Header.Set("Authorization", header)
	}
	return r
}

func TestAuthenticateBearer(t *testing.T) {
	a := testAuth(t)
	p, ok := a.Authenticate(request("Bearer " + tokenA))
	if !ok || p.Name != "alice" || p.Tenant != "team-a" {
		t.Fatalf("got %+v %v", p, ok)
	}
	p, ok = a.Authenticate(request("bearer " + tokenB))
	if !ok || p.Name != "bob" {
		t.Fatalf("scheme is case-insensitive: %+v %v", p, ok)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	a := testAuth(t)
	for name, h := range map[string]string{
		"empty": "", "wrong": "Bearer " + tokenA + "x", "prefix": "Bearer " + tokenA[:10],
		"basic": "Basic " + tokenA, "no scheme": tokenA, "bare bearer": "Bearer ", "two": "Bearer " + tokenA + " " + tokenB,
	} {
		if _, ok := a.Authenticate(request(h)); ok {
			t.Errorf("%s: must not authenticate", name)
		}
	}
}

func TestTokenInQueryStringIsIgnored(t *testing.T) {
	a := testAuth(t)
	r := httptest.NewRequest(http.MethodPost, "http://x/?access_token="+tokenA, nil)
	if _, ok := a.Authenticate(r); ok {
		t.Fatal("tokens in URLs leak into logs; only the Authorization header counts")
	}
}

func TestWrapRejectsWith401AndChallenge(t *testing.T) {
	called := false
	h := testAuth(t).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }), func(*http.Request) bool { return false })
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request(""))
	if rec.Code != http.StatusUnauthorized || called {
		t.Fatalf("code %d called %v", rec.Code, called)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
}

func TestWrapPassesPrincipalAndPublicPaths(t *testing.T) {
	var got Principal
	h := testAuth(t).Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = PrincipalFrom(r.Context())
	}), func(r *http.Request) bool { return r.URL.Path == "/public" })
	h.ServeHTTP(httptest.NewRecorder(), request("Bearer "+tokenA))
	if got.Name != "alice" {
		t.Fatalf("principal %+v", got)
	}
	got = Principal{}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://x/public", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("public path code %d", rec.Code)
	}
}

func TestPrincipalKeysIsolate(t *testing.T) {
	seen := map[string]Principal{}
	for _, p := range []Principal{
		{Name: "alice", Tenant: "team-a"}, {Name: "bob", Tenant: "team-a"}, {Name: "alice"}, {Name: "bob"}, {Name: "team-a"},
	} {
		k := p.Key()
		if k == "" {
			t.Fatalf("%+v has no key", p)
		}
		if other, dup := seen[k]; dup && !(other.Tenant != "" && p.Tenant == other.Tenant) {
			t.Fatalf("%+v and %+v share key %q", p, other, k)
		}
		seen[k] = p
	}
	if (Principal{Name: "alice", Tenant: "t"}).Key() != (Principal{Name: "bob", Tenant: "t"}).Key() {
		t.Fatal("two tokens of one tenant share tasks and contexts")
	}
	if (Principal{Name: "team-a"}).Key() == (Principal{Name: "x", Tenant: "team-a"}).Key() {
		t.Fatal("a token named like a tenant must not collide with the tenant")
	}
}

func TestInsecureNoAuthGivesOneAnonymousPrincipal(t *testing.T) {
	a, err := NewAuthenticator(nil, envFrom(nil), true)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := a.Authenticate(request(""))
	if !ok || p.Name != "anonymous" {
		t.Fatalf("%+v %v", p, ok)
	}
}

func TestNoTokensNoInsecureFlagIsAnError(t *testing.T) {
	if _, err := NewAuthenticator(nil, envFrom(nil), false); err == nil {
		t.Fatal("an authenticator with no tokens would reject everyone or accept everyone; refuse to build it")
	}
}
