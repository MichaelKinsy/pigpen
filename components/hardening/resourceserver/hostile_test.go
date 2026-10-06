package resourceserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/MichaelKinsy/pigpen/components/hardening/profile"
)

// A token whose signature is valid but whose size is over the cap is refused on size alone.
func TestAnOversizeTokenWithAValidSignatureIsRefused(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	small := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"pad": strings.Repeat("x", 3000)}))
	if _, err := e.v.Verify(context.Background(), small); err != nil {
		t.Fatalf("control: %v", err)
	}
	big := sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"pad": strings.Repeat("x", MaxTokenBytes)}))
	if len(big) <= MaxTokenBytes {
		t.Fatalf("token is %d bytes", len(big))
	}
	_, err := e.v.Verify(context.Background(), big)
	wantCode(t, err, profile.TokenInvalid)
	if e.idp.hits.Load() != 1 { // only the control fetched
		t.Fatalf("%d fetches", e.idp.hits.Load())
	}
}

// A repeated header key is ambiguous, whatever its value: the token is refused although it is correctly signed.
func TestARepeatedHeaderKeyIsRefusedEvenWithAValidSignature(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	claims, _ := json.Marshal(e.claims(nil))
	build := func(header string) string {
		input := b64([]byte(header)) + "." + b64(claims)
		return input + "." + b64(rs256(rsaKey)([]byte(input)))
	}
	if _, err := e.v.Verify(context.Background(), build(`{"alg":"RS256","kid":"k1"}`)); err != nil {
		t.Fatalf("control: %v", err)
	}
	for name, header := range map[string]string{
		"alg twice": `{"alg":"RS256","alg":"RS256","kid":"k1"}`,
		"kid twice": `{"alg":"RS256","kid":"k1","kid":"k1"}`,
		"typ twice": `{"alg":"RS256","kid":"k1","typ":"JWT","typ":"JWT"}`,
	} {
		_, err := e.v.Verify(context.Background(), build(header))
		if !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Each array element must equal the audience; one that merely starts with it does not count.
func TestAnAudienceArrayElementMustEqualNotStartWith(t *testing.T) {
	e := newEnv(t, pub("k1", &rsaKey.PublicKey))
	for _, aud := range []any{testAudience + "-extra", []string{testAudience + "-extra"}, []string{"x", testAudience + "/admin"}, []string{testAudience[:1]}} {
		_, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k1", nil, e.claims(map[string]any{"aud": aud})))
		if !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%v: %v", aud, err)
		}
	}
}

// ES256 needs a P-256 key, ES384 a P-384 key and ES512 a P-521 key: a key on the wrong curve is refused as an
// algorithm mismatch, not tried.
func TestTheCurveMustMatchTheAlgorithm(t *testing.T) {
	cases := []struct {
		alg       jose.SignatureAlgorithm
		signWith  any
		published any
	}{
		{jose.ES256, ecKey, &ecKey384.PublicKey},
		{jose.ES256, ecKey, &ecKey521.PublicKey},
		{jose.ES384, ecKey384, &ecKey.PublicKey},
		{jose.ES384, ecKey384, &ecKey521.PublicKey},
		{jose.ES512, ecKey521, &ecKey.PublicKey},
		{jose.ES512, ecKey521, &ecKey384.PublicKey},
	}
	for _, c := range cases {
		e := newEnv(t, pub("k", c.published))
		e.v = e.verifier(func(r *profile.ResourceServer) { r.Algorithms = []string{"ES256", "ES384", "ES512"} })
		_, err := e.v.Verify(context.Background(), sign(t, c.alg, c.signWith, "k", nil, e.claims(nil)))
		if !profile.IsCode(err, profile.TokenAlgDenied) {
			t.Errorf("%s with a mismatched key: %v", c.alg, err)
		}
	}
	// the control: the matching curves verify
	for _, c := range []struct {
		alg jose.SignatureAlgorithm
		key any
		pub any
	}{{jose.ES256, ecKey, &ecKey.PublicKey}, {jose.ES384, ecKey384, &ecKey384.PublicKey}, {jose.ES512, ecKey521, &ecKey521.PublicKey}} {
		e := newEnv(t, pub("k", c.pub))
		e.v = e.verifier(func(r *profile.ResourceServer) { r.Algorithms = []string{"ES256", "ES384", "ES512"} })
		if _, err := e.v.Verify(context.Background(), sign(t, c.alg, c.key, "k", nil, e.claims(nil))); err != nil {
			t.Errorf("%s: %v", c.alg, err)
		}
	}
	// An EC key never verifies an RSA algorithm, and an RSA key never an EC one.
	e := newEnv(t, pub("k", &ecKey.PublicKey))
	_, err := e.v.Verify(context.Background(), sign(t, jose.RS256, rsaKey, "k", nil, e.claims(nil)))
	wantCode(t, err, profile.TokenAlgDenied)
}

func TestAJWKSOverTheSizeCapIsRefusedEvenWhenItHoldsAKey(t *testing.T) {
	e := newEnv(t)
	goodJSON, _ := json.Marshal(pub("k1", &rsaKey.PublicKey))
	e.idp.setRaw(`{"keys":[` + string(goodJSON) + `],"pad":"` + strings.Repeat("x", MaxJWKSBytes) + `"}`)
	wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
	e.idp.setRaw(`{"keys":[` + string(goodJSON) + `],"pad":"` + strings.Repeat("x", MaxJWKSBytes/2) + `"}`)
	e.clk.Add(FailedRefreshBackoff + 1)
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatalf("a JWKS under the cap: %v", err)
	}
}

// The cap is exact: a document of MaxJWKSBytes bytes is read, one of MaxJWKSBytes+1 bytes is not.
func TestTheJWKSSizeCapIsExact(t *testing.T) {
	goodJSON, _ := json.Marshal(pub("k1", &rsaKey.PublicKey))
	sized := func(n int) string {
		head, tail := `{"keys":[`+string(goodJSON)+`],"pad":"`, `"}`
		return head + strings.Repeat("x", n-len(head)-len(tail)) + tail
	}
	e := newEnv(t)
	e.idp.setRaw(sized(MaxJWKSBytes + 1))
	wantCode(t, e.verify("k1", rsaKey, jose.RS256), profile.JWKSUnavailable)
	e = newEnv(t)
	e.idp.setRaw(sized(MaxJWKSBytes))
	if err := e.verify("k1", rsaKey, jose.RS256); err != nil {
		t.Fatalf("a document at the cap: %v", err)
	}
}

// A kid that is not a non-empty string is refused outright. It must not select a key that has no kid of its own.
func TestAMalformedKidDoesNotSelectAKeyWithoutOne(t *testing.T) {
	e := newEnv(t, pub("", &rsaKey.PublicKey)) // a JWKS whose only key has no kid
	if err := e.verify("", rsaKey, jose.RS256); err != nil {
		t.Fatalf("control (no kid, one key): %v", err)
	}
	for name, kid := range map[string]any{"number": 7, "empty": "", "null": nil, "array": []string{"k"}, "object": map[string]any{}} {
		tok := craft(t, map[string]any{"alg": "RS256", "kid": kid}, e.claims(nil), rs256(rsaKey))
		if _, err := e.v.Verify(context.Background(), tok); !profile.IsCode(err, profile.TokenInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
