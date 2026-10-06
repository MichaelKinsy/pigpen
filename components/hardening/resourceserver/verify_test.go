package resourceserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

func TestAValidTokenIsAdmittedAndMapsToAPrincipal(t *testing.T) {
	for name, tc := range map[string]struct {
		alg jose.SignatureAlgorithm
		key any
		pub any
	}{
		"RS256": {jose.RS256, rsaKey, &rsaKey.PublicKey},
		"ES256": {jose.ES256, ecKey, &ecKey.PublicKey},
	} {
		e := newEnv(t, pub("k1", tc.pub))
		tok := sign(t, tc.alg, tc.key, "k1", nil, e.claims(nil))
		p, err := e.v.Verify(context.Background(), tok)
		if err != nil || p != (Principal{Subject: "alice", Tenant: "acme"}) {
			t.Fatalf("%s: %+v %v", name, p, err)
		}
	}
}

func TestEveryAllowedAlgorithmVerifies(t *testing.T) {
	cases := []struct {
		alg jose.SignatureAlgorithm
		key any
		pub any
	}{
		{jose.RS384, rsaKey, &rsaKey.PublicKey}, {jose.RS512, rsaKey, &rsaKey.PublicKey},
		{jose.PS256, rsaKey, &rsaKey.PublicKey}, {jose.PS384, rsaKey, &rsaKey.PublicKey}, {jose.PS512, rsaKey, &rsaKey.PublicKey},
		{jose.ES384, ecKey384, &ecKey384.PublicKey}, {jose.ES512, ecKey521, &ecKey521.PublicKey}, {jose.EdDSA, edKey, edKey.Public()},
	}
	for _, c := range cases {
		e := newEnv(t, pub("k", c.pub))
		e.v = e.verifier(func(r *profile.ResourceServer) { r.Algorithms = []string{string(c.alg)} })
		if _, err := e.v.Verify(context.Background(), sign(t, c.alg, c.key, "k", nil, e.claims(nil))); err != nil {
			t.Errorf("%s: %v", c.alg, err)
		}
	}
}

func TestNoTokenAndMalformedTokens(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	good := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))
	parts := strings.Split(good, ".")
	for name, tok := range map[string]string{
		"empty":              "",
		"one segment":        parts[0],
		"two segments":       parts[0] + "." + parts[1],
		"four segments":      good + ".x",
		"whitespace":         " " + good,
		"newline inside":     good[:20] + "\n" + good[20:],
		"trailing space":     good + " ",
		"padding":            parts[0] + "=." + parts[1] + "." + parts[2],
		"not base64":         "!!!." + parts[1] + "." + parts[2],
		"json serialization": `{"payload":"` + parts[1] + `","protected":"` + parts[0] + `","signature":"` + parts[2] + `"}`,
		"bearer prefix":      "Bearer " + good,
		"header not json":    b64([]byte("nope")) + "." + parts[1] + "." + parts[2],
		"header array":       b64([]byte("[]")) + "." + parts[1] + "." + parts[2],
		"header null":        b64([]byte("null")) + "." + parts[1] + "." + parts[2],
		"JWE shape":          parts[0] + ".a.b.c." + parts[2],
		"oversize":           parts[0] + "." + b64([]byte(strings.Repeat("x", MaxTokenBytes))) + "." + parts[2],
	} {
		_, err := e.v.Verify(context.Background(), tok)
		if code := profile.CodeOf(err); code != profile.TokenInvalid && code != profile.TokenAlgDenied {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.v.Verify(context.Background(), good); err != nil {
		t.Fatalf("the control token: %v", err)
	}
	// a token at exactly the cap is parsed (and then fails for another reason), one byte more is refused first
	if _, err := e.v.Verify(context.Background(), strings.Repeat("a", MaxTokenBytes+1)); !profile.IsCode(err, profile.TokenInvalid) {
		t.Fatal(err)
	}
	if e.idp.hits.Load() != 1 {
		t.Fatalf("a malformed token fetched the JWKS (%d fetches)", e.idp.hits.Load())
	}
}

func TestBadSignature(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	// signed by another key under the right kid
	_, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey2, "k1", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenInvalid)
	// tampered payload
	good := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))
	parts := strings.Split(good, ".")
	evil, _ := json.Marshal(e.claims(map[string]any{"sub": "mallory"}))
	_, err = e.v.Verify(context.Background(), parts[0]+"."+b64(evil)+"."+parts[2])
	wantCode(t, err, profile.TokenInvalid)
	// emptied and truncated signatures
	_, err = e.v.Verify(context.Background(), parts[0]+"."+parts[1]+".")
	wantCode(t, err, profile.TokenInvalid)
	_, err = e.v.Verify(context.Background(), parts[0]+"."+parts[1]+"."+parts[2][:len(parts[2])/2])
	wantCode(t, err, profile.TokenInvalid)
}

func TestAlgorithmAttacks(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	claims := e.claims(nil)
	// alg none, in every spelling
	for _, alg := range []string{"none", "None", "NONE", "nOnE", ""} {
		tok := craft(t, map[string]any{"alg": alg, "kid": "k1"}, claims, nil)
		_, err := e.v.Verify(context.Background(), tok)
		wantCode(t, err, profile.TokenAlgDenied)
	}
	tok := craft(t, map[string]any{"kid": "k1"}, claims, nil) // no alg at all
	_, err := e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenAlgDenied)
	tok = craft(t, map[string]any{"alg": 5, "kid": "k1"}, claims, nil)
	_, err = e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenAlgDenied)
	// HS256 signed with the public key as the secret, in every form of that key
	pem := publicKeyPEM(t, &rsaKey.PublicKey)
	for _, secret := range [][]byte{pem, []byte(strings.TrimSpace(string(pem))), []byte(testIssuer)} {
		for _, alg := range []string{"HS256", "HS384", "HS512"} {
			tok := craft(t, map[string]any{"alg": alg, "kid": "k1"}, claims, hs256(secret))
			_, err := e.v.Verify(context.Background(), tok)
			wantCode(t, err, profile.TokenAlgDenied)
		}
	}
	// an algorithm that is real but not configured
	_, err = e.v.Verify(context.Background(), sign(t, jose.RS512, rsaKey, "k1", nil, claims))
	wantCode(t, err, profile.TokenAlgDenied)
	// the lower-case spelling is a different algorithm
	_, err = e.v.Verify(context.Background(), craft(t, map[string]any{"alg": "rs256", "kid": "k1"}, claims, rs256(rsaKey)))
	wantCode(t, err, profile.TokenAlgDenied)
	// a repeated alg key is ambiguous
	h := `{"alg":"none","alg":"RS256","kid":"k1"}`
	c, _ := json.Marshal(claims)
	_, err = e.v.Verify(context.Background(), b64([]byte(h))+"."+b64(c)+".")
	wantCode(t, err, profile.TokenInvalid)
	if e.idp.hits.Load() != 0 {
		t.Fatalf("an algorithm attack fetched the JWKS %d times", e.idp.hits.Load())
	}
}

func TestTheAlgorithmMustMatchTheKey(t *testing.T) {
	e := newEnv(t, pub("rsa", &rsaKey.PublicKey), pub("ec", &ecKey.PublicKey))
	// ES256 token whose kid names the RSA key, and an RS256 token whose kid names the EC key
	_, err := e.v.Verify(context.Background(), sign(t, jose.ES256, ecKey, "rsa", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
	_, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "ec", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
	// a key whose own alg says otherwise
	e = newEnv(t, pub("k", &rsaKey.PublicKey, func(k *jose.JSONWebKey) { k.Algorithm = "RS512" }))
	_, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
	e = newEnv(t, pub("k", &rsaKey.PublicKey, func(k *jose.JSONWebKey) { k.Algorithm = "RS256" }))
	if _, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k", nil, e.claims(nil))); err != nil {
		t.Fatal(err)
	}
	// a key meant for encryption
	e = newEnv(t, pub("k", &rsaKey.PublicKey, func(k *jose.JSONWebKey) { k.Use = "enc" }))
	_, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
	e = newEnv(t, pub("k", &rsaKey.PublicKey, func(k *jose.JSONWebKey) { k.Use = "sig" }))
	if _, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k", nil, e.claims(nil))); err != nil {
		t.Fatal(err)
	}
	// a 1024-bit RSA key is not accepted
	e = newEnv(t, pub("k", &rsaSmall.PublicKey))
	_, err = e.v.Verify(context.Background(), sign(t, jose.RS256, rsaSmall, "k", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
	// a P-384 key does not verify ES256
	e = newEnv(t, pub("k", &ecKey384.PublicKey))
	_, err = e.v.Verify(context.Background(), sign(t, jose.ES384, ecKey384, "k", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied) // ES384 is not configured
}

func TestHeaderFieldsThatPointElsewhereAreIgnored(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	evil := newIDP(t, pub("k1", &rsaKey2.PublicKey)) // an attacker's JWKS, with the same kid
	// jku and x5u pointing at the attacker's server: never fetched, and the token (signed by the attacker) fails.
	for _, hdr := range []map[string]any{
		{"jku": evil.url()}, {"x5u": evil.url()}, {"jku": evil.url(), "x5u": evil.url()},
	} {
		tok := sign(t, jose.RS256, rsaKey2, "k1", hdr, e.claims(nil))
		_, err := e.v.Verify(context.Background(), tok)
		wantCode(t, err, profile.TokenInvalid)
	}
	// the same headers on a token signed by the real key change nothing
	tok := sign(t, jose.RS256, rsaKey, "k1", map[string]any{"jku": evil.url(), "x5u": evil.url()}, e.claims(nil))
	if _, err := e.v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("ignored headers broke a valid token: %v", err)
	}
	if evil.hits.Load() != 0 {
		t.Fatal("the jku or x5u URL was fetched")
	}
	// an embedded jwk (the attacker's own public key) is not used to verify
	attacker := jose.JSONWebKey{Key: &rsaKey2.PublicKey, KeyID: "k1"}
	raw, _ := json.Marshal(attacker)
	var jwkObj map[string]any
	_ = json.Unmarshal(raw, &jwkObj)
	tok = craft(t, map[string]any{"alg": "RS256", "kid": "k1", "jwk": jwkObj}, e.claims(nil), rs256(rsaKey2))
	_, err := e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenInvalid)
	// also with no kid at all, so that the embedded key would be the only candidate
	tok = craft(t, map[string]any{"alg": "RS256", "jwk": jwkObj}, e.claims(nil), rs256(rsaKey2))
	_, err = e.v.Verify(context.Background(), tok)
	wantCode(t, err, profile.TokenInvalid)
}

func TestACriticalHeaderIsRejected(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	for name, crit := range map[string]any{"unknown": []string{"x-unknown"}, "exp": []string{"exp"}, "b64": []string{"b64"}, "empty": []string{}, "not a list": "x"} {
		tok := craft(t, map[string]any{"alg": "RS256", "kid": "k1", "crit": crit, "x-unknown": 1, "b64": true}, e.claims(nil), rs256(rsaKey))
		_, err := e.v.Verify(context.Background(), tok)
		if !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	tok := craft(t, map[string]any{"alg": "RS256", "kid": "k1", "b64": false}, e.claims(nil), rs256(rsaKey))
	if _, err := e.v.Verify(context.Background(), tok); !profile.IsCode(err, profile.TokenInvalid) {
		t.Errorf("b64: %v", err)
	}
	// the control: the same hand-made token without those headers verifies
	tok = craft(t, map[string]any{"alg": "RS256", "kid": "k1", "typ": "JWT"}, e.claims(nil), rs256(rsaKey))
	if _, err := e.v.Verify(context.Background(), tok); err != nil {
		t.Fatalf("control: %v", err)
	}
}

func TestIssuerAndAudience(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	check := func(name string, over map[string]any, ok bool) {
		t.Helper()
		_, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(over)))
		if ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !ok && !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	check("valid", nil, true)
	check("aud array containing it", map[string]any{"aud": []string{"other", testAudience}}, true)
	check("aud array without it", map[string]any{"aud": []string{"other", "another"}}, false)
	check("aud array empty", map[string]any{"aud": []string{}}, false)
	check("aud different string", map[string]any{"aud": "other"}, false)
	check("aud case differs", map[string]any{"aud": "A2A"}, false)
	check("aud prefix", map[string]any{"aud": "a2a-extra"}, false)
	check("aud suffix of array element", map[string]any{"aud": []string{"x" + testAudience}}, false)
	check("aud with trailing space", map[string]any{"aud": testAudience + " "}, false)
	check("aud missing", map[string]any{"aud": nil}, false)
	check("aud number", map[string]any{"aud": 5}, false)
	check("aud array with a number", map[string]any{"aud": []any{testAudience, 5}}, false)
	check("iss with trailing slash dropped", map[string]any{"iss": strings.TrimSuffix(testIssuer, "/")}, false)
	check("iss with extra slash", map[string]any{"iss": testIssuer + "/"}, false)
	check("iss other host", map[string]any{"iss": "https://evil.example/"}, false)
	check("iss case differs", map[string]any{"iss": "https://IDP.example/"}, false)
	check("iss missing", map[string]any{"iss": nil}, false)
	check("iss array", map[string]any{"iss": []string{testIssuer}}, false)
	// a configuration without the trailing slash is its own exact string
	e.v = e.verifier(func(r *profile.ResourceServer) { r.Issuer = strings.TrimSuffix(testIssuer, "/") })
	check("config without slash, token with slash", nil, false)
	check("config without slash, token without", map[string]any{"iss": strings.TrimSuffix(testIssuer, "/")}, true)
}

func TestTimeClaimsAndLeeway(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	now := e.clk.Now()
	at := func(d time.Duration) int64 { return now.Add(d).Unix() }
	check := func(name string, over map[string]any, ok bool) {
		t.Helper()
		_, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(over)))
		if ok && err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if !ok && !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	check("exp far future", nil, true)
	check("exp just in the future", map[string]any{"exp": at(time.Second)}, true)
	check("exp now (inside the leeway)", map[string]any{"exp": at(0)}, true)
	check("exp 59 s ago (inside the leeway)", map[string]any{"exp": at(-59 * time.Second)}, true)
	check("exp exactly at the leeway", map[string]any{"exp": at(-60 * time.Second)}, false)
	check("exp 61 s ago (outside)", map[string]any{"exp": at(-61 * time.Second)}, false)
	check("exp long ago", map[string]any{"exp": at(-24 * time.Hour)}, false)
	check("exp missing", map[string]any{"exp": nil}, false)
	check("exp is a string", map[string]any{"exp": fmt.Sprint(at(time.Hour))}, false)
	check("exp is a bool", map[string]any{"exp": true}, false)
	check("exp negative", map[string]any{"exp": -1}, false)
	check("exp absurd", map[string]any{"exp": 1e300}, false)
	check("exp fractional", map[string]any{"exp": float64(at(time.Hour)) + 0.5}, true)
	check("nbf missing", map[string]any{"nbf": nil}, true)
	check("nbf in the past", map[string]any{"nbf": at(-time.Hour)}, true)
	check("nbf now", map[string]any{"nbf": at(0)}, true)
	check("nbf 59 s ahead (inside the leeway)", map[string]any{"nbf": at(59 * time.Second)}, true)
	check("nbf exactly at the leeway", map[string]any{"nbf": at(60 * time.Second)}, true)
	check("nbf 61 s ahead (outside)", map[string]any{"nbf": at(61 * time.Second)}, false)
	check("nbf a day ahead", map[string]any{"nbf": at(24 * time.Hour)}, false)
	check("nbf is a string", map[string]any{"nbf": "0"}, false)
	check("iat missing", map[string]any{"iat": nil}, true)
	check("iat 59 s ahead", map[string]any{"iat": at(59 * time.Second)}, true)
	check("iat 61 s ahead", map[string]any{"iat": at(61 * time.Second)}, false)
	check("iat is null", map[string]any{"iat": json.RawMessage("null")}, false)
	// the leeway is the profile's
	e.v = e.verifier(func(r *profile.ResourceServer) { r.Leeway = 0 })
	check("zero leeway: exp 1 s ago", map[string]any{"exp": at(-time.Second)}, false)
	check("zero leeway: exp 1 s ahead", map[string]any{"exp": at(time.Second)}, true)
	check("zero leeway: nbf 1 s ahead", map[string]any{"nbf": at(time.Second)}, false)
	e.v = e.verifier(func(r *profile.ResourceServer) { r.Leeway = 300 * time.Second })
	check("max leeway: exp 299 s ago", map[string]any{"exp": at(-299 * time.Second)}, true)
	check("max leeway: exp 301 s ago", map[string]any{"exp": at(-301 * time.Second)}, false)
	// time passes: a token valid now is invalid later
	e.v = e.verifier(func(r *profile.ResourceServer) {})
	tok := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"exp": at(30 * time.Minute)}))
	if _, err := e.v.Verify(context.Background(), tok); err != nil {
		t.Fatal(err)
	}
	e.clk.Add(31*time.Minute + 61*time.Second)
	if _, err := e.v.Verify(context.Background(), tok); !profile.IsCode(err, profile.TokenInvalid) {
		t.Fatalf("an expired token was accepted: %v", err)
	}
}

func TestSubjectAndTenantClaims(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	check := func(name string, over map[string]any, want Principal, ok bool) {
		t.Helper()
		p, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(over)))
		if ok && (err != nil || p != want) {
			t.Errorf("%s: %+v %v", name, p, err)
		}
		if !ok && (!profile.IsCode(err, profile.TokenInvalid) || p != (Principal{})) {
			t.Errorf("%s: %+v %v", name, p, err)
		}
	}
	check("plain", nil, Principal{"alice", "acme"}, true)
	check("punctuation", map[string]any{"sub": "a.b_c-d", "tenant": "T-1.x_y"}, Principal{"a.b_c-d", "T-1.x_y"}, true)
	check("64 characters", map[string]any{"sub": strings.Repeat("a", 64)}, Principal{strings.Repeat("a", 64), "acme"}, true)
	check("65 characters", map[string]any{"sub": strings.Repeat("a", 65)}, Principal{}, false)
	for name, v := range map[string]string{"slash": "ac/me", "traversal": "../acme", "space": "a b", "colon": "a:b", "empty": "", "newline": "a\nb", "at": "a@b", "unicode": "älice", "nul": "a\u0000"} {
		check("tenant "+name, map[string]any{"tenant": v}, Principal{}, false)
		check("sub "+name, map[string]any{"sub": v}, Principal{}, false)
	}
	check("sub missing", map[string]any{"sub": nil}, Principal{}, false)
	check("tenant missing", map[string]any{"tenant": nil}, Principal{}, false)
	check("sub number", map[string]any{"sub": 7}, Principal{}, false)
	check("tenant array", map[string]any{"tenant": []string{"acme"}}, Principal{}, false)
	// configured claim names
	e.v = e.verifier(func(r *profile.ResourceServer) { r.SubjectClaim, r.TenantClaim = "user", "https://example.com/tenant" })
	check("custom claims", map[string]any{"user": "bob", "https://example.com/tenant": "globex", "sub": nil, "tenant": nil}, Principal{"bob", "globex"}, true)
	check("the default claim names are not used", nil, Principal{}, false)
}

func TestClaimsAreStrict(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	sig := func(payload string) string {
		return craft2(t, map[string]any{"alg": "RS256", "kid": "k1"}, payload, rs256(rsaKey))
	}
	now := e.clk.Now().Unix()
	base := fmt.Sprintf(`"iss":%q,"aud":%q,"sub":"alice","tenant":"acme","exp":%d`, testIssuer, testAudience, now+3600)
	if _, err := e.v.Verify(context.Background(), sig(`{`+base+`}`)); err != nil {
		t.Fatalf("control: %v", err)
	}
	for name, payload := range map[string]string{
		"duplicate sub":    `{` + base + `,"sub":"mallory"}`,
		"duplicate exp":    `{` + base + `,"exp":1}`,
		"duplicate aud":    `{` + base + `,"aud":"other"}`,
		"payload array":    `[]`,
		"payload null":     `null`,
		"payload string":   `"x"`,
		"payload not json": `nope`,
		"empty payload":    ``,
		"trailing value":   `{` + base + `} {}`,
	} {
		if _, err := e.v.Verify(context.Background(), sig(payload)); !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func craft2(t *testing.T, header map[string]any, payload string, signFn func([]byte) []byte) string {
	t.Helper()
	h, _ := json.Marshal(header)
	input := b64(h) + "." + b64([]byte(payload))
	return input + "." + b64(signFn([]byte(input)))
}

func TestErrorsNameNoTokenClaimOrKey(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	secret := "SECRET-SUBJECT"
	tokens := []string{
		sign(t, jose.RS256, rsaKey2, "k1", nil, e.claims(map[string]any{"sub": secret})),
		sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"sub": secret, "aud": "SECRET-AUD"})),
		sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"tenant": "SECRET/TENANT"})),
		sign(t, jose.RS256, rsaKey, "unknown-kid-SECRET", nil, e.claims(nil)),
		craft(t, map[string]any{"alg": "HS256", "kid": "SECRET-KID"}, e.claims(map[string]any{"sub": secret}), hs256([]byte("x"))),
	}
	for _, tok := range tokens {
		_, err := e.v.Verify(context.Background(), tok)
		if err == nil {
			t.Fatal("accepted")
		}
		for _, leak := range []string{"SECRET", tok[:20], testIssuer, "idp.example", e.idp.url(), "k1"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %q contains %q", err.Error(), leak)
			}
		}
	}
}

func TestCancelledContext(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := e.v.Verify(ctx, sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil)))
	wantCode(t, err, profile.Cancelled)
	if e.idp.hits.Load() != 0 {
		t.Fatal("fetched for a cancelled request")
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	good := func() *profile.ResourceServer {
		return &profile.ResourceServer{Issuer: "i", Audience: "a", JWKSURL: "http://127.0.0.1/k", Algorithms: []string{"RS256"}, Leeway: time.Minute, SubjectClaim: "sub", TenantClaim: "tenant"}
	}
	if _, err := New("a2a", good(), nil); err != nil {
		t.Fatal(err)
	}
	for name, mod := range map[string]func(*profile.ResourceServer){
		"no issuer": func(c *profile.ResourceServer) { c.Issuer = "" }, "no audience": func(c *profile.ResourceServer) { c.Audience = "" },
		"no url": func(c *profile.ResourceServer) { c.JWKSURL = "" }, "no algorithms": func(c *profile.ResourceServer) { c.Algorithms = nil },
		"HS256": func(c *profile.ResourceServer) { c.Algorithms = []string{"HS256"} }, "none": func(c *profile.ResourceServer) { c.Algorithms = []string{"RS256", "none"} },
		"leeway": func(c *profile.ResourceServer) { c.Leeway = 301 * time.Second }, "negative leeway": func(c *profile.ResourceServer) { c.Leeway = -1 },
		"no sub claim": func(c *profile.ResourceServer) { c.SubjectClaim = "" }, "no tenant claim": func(c *profile.ResourceServer) { c.TenantClaim = "" },
	} {
		c := good()
		mod(c)
		if _, err := New("a2a", c, nil); !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := New("a2a", nil, nil); !profile.IsCode(err, profile.ProfileMisconfigured) {
		t.Error("nil config")
	}
	for _, u := range []string{"http://idp.example/k", "ftp://idp.example/k", "https://u:p@idp.example/k", "https://idp.example/k?x=1", "https://idp.example/k#f", "idp.example/k", "https://", "file:///etc/jwks"} {
		c := good()
		c.JWKSURL = u
		if _, err := New("a2a", c, nil); !profile.IsCode(err, profile.ProfileMisconfigured) {
			t.Errorf("%s: %v", u, err)
		}
	}
	for _, u := range []string{"https://idp.example/k", "http://127.0.0.1:8080/k", "http://localhost/k", "http://[::1]:9/k"} {
		c := good()
		c.JWKSURL = u
		if _, err := New("a2a", c, nil); err != nil {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestNewDoesNoNetworkWork(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	_ = e.verifier(func(*profile.ResourceServer) {})
	if e.idp.hits.Load() != 0 {
		t.Fatal("New fetched the JWKS")
	}
}

func TestConcurrentVerification(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	tok := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.v.Verify(context.Background(), tok); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if e.idp.hits.Load() != 1 {
		t.Fatalf("%d JWKS fetches for a concurrent burst, want 1", e.idp.hits.Load())
	}
}

func TestAuthenticateAndHTTPHelpers(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	tok := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(nil))
	req := httptest.NewRequest(http.MethodPost, "/tasks", nil)
	req.Header.Set("Authorization", "bearer "+tok)
	p, err := e.v.Authenticate(req)
	if err != nil || p.Subject != "alice" {
		t.Fatalf("%+v %v", p, err)
	}
	for name, hdr := range map[string][]string{
		"none": nil, "basic": {"Basic " + tok}, "no token": {"Bearer"}, "empty token": {"Bearer "}, "two tokens": {"Bearer " + tok + " extra"},
		"two headers": {"Bearer " + tok, "Bearer " + tok}, "raw token": {tok}, "tab": {"Bearer\t" + tok},
	} {
		r := httptest.NewRequest(http.MethodPost, "/tasks", nil)
		for _, h := range hdr {
			r.Header.Add("Authorization", h)
		}
		if _, err := e.v.Authenticate(r); !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// status mapping and the response
	for code, status := range map[profile.Code]int{profile.TokenInvalid: 401, profile.TokenAlgDenied: 401, profile.JWKSUnavailable: 503, profile.Cancelled: 503, profile.CredentialExpired: 401} {
		err := profile.NewError(code, profile.FlagResourceServer, "a2a")
		if StatusFor(err) != status {
			t.Errorf("%s: %d", code, StatusFor(err))
		}
		w := httptest.NewRecorder()
		WriteError(w, err)
		if w.Code != status || (status == 401) != (w.Header().Get("WWW-Authenticate") == "Bearer") || strings.Contains(w.Body.String(), "SECRET") {
			t.Errorf("%s: %d %v %q", code, w.Code, w.Header(), w.Body.String())
		}
		if !strings.HasPrefix(w.Body.String(), string(code)+": ") {
			t.Errorf("%s: body %q", code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	WriteError(w, fmt.Errorf("an untyped error naming SECRET"))
	if w.Code != 401 || strings.Contains(w.Body.String(), "SECRET") || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}
