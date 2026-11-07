package langsmith

import (
	"context"
	"errors"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestProfileUTF8DecodeVectors(t *testing.T) {
	var v struct {
		UTF8 []struct {
			Hex string `json:"hex"`
			Out string `json:"out"`
		} `json:"utf8"`
	}
	loadProfileGolden(t, "profile_vectors.json", &v)
	if len(v.UTF8) < 400 {
		t.Fatalf("vectors truncated: %d", len(v.UTF8))
	}
	for _, c := range v.UTF8 {
		b := make([]byte, len(c.Hex)/2)
		for i := range b {
			b[i] = byte(pfHexVal(c.Hex[2*i])<<4 | pfHexVal(c.Hex[2*i+1]))
		}
		if got := decodeUTF8Replacing(b); got != c.Out {
			t.Errorf("decode(%s) = %q want %q", c.Hex, got, c.Out)
		}
	}
}

func TestProfileToString(t *testing.T) {
	for _, c := range []struct {
		v    any
		want string
	}{
		{nil, "null"}, {undef, "undefined"}, {true, "true"}, {false, "false"}, {"s", "s"}, {"", ""},
		{float64(0), "0"}, {math.Copysign(0, -1), "0"}, {1.5, "1.5"}, {1e21, "1e+21"}, {1e-7, "1e-7"}, {-12345.0, "-12345"},
		{math.NaN(), "NaN"}, {math.Inf(1), "Infinity"}, {math.Inf(-1), "-Infinity"},
		{newOmap(), "[object Object]"}, {[]any{}, ""}, {[]any{"a"}, "a"},
		{[]any{1.0, []any{2.0, nil, 3.0}, nil, undef, "x"}, "1,2,,3,,,x"},
		{[]any{newOmap(), true}, "[object Object],true"},
		{struct{}{}, ""},
	} {
		if got := pfToString(c.v); got != c.want {
			t.Errorf("String(%#v) = %q want %q", c.v, got, c.want)
		}
	}
}

func TestProfileTruthy(t *testing.T) {
	for v, want := range map[string]bool{"nil": false, "undef": false, "false": false, "zero": false, "nan": false, "empty": false,
		"true": true, "one": true, "str": true, "obj": true, "arr": true, "emptyArr": true} {
		var in any
		switch v {
		case "nil":
		case "undef":
			in = undef
		case "false":
			in = false
		case "zero":
			in = float64(0)
		case "nan":
			in = math.NaN()
		case "empty":
			in = ""
		case "true":
			in = true
		case "one":
			in = float64(1)
		case "str":
			in = "x"
		case "obj":
			in = newOmap()
		case "arr", "emptyArr":
			in = []any{}
		}
		if got := pfTruthy(in); got != want {
			t.Errorf("truthy(%s) = %v", v, got)
		}
	}
}

func TestProfileIssuerThatDoesNotParseIsUntrusted(t *testing.T) {
	doc := obj("issuer", "not a url", "device_authorization_endpoint", "http://h/d", "token_endpoint", "http://h/t")
	if isTrustedOAuthMetadata(doc, "not a url") {
		t.Fatal("trusted")
	}
	if isTrustedOAuthMetadata(newOmap(), "x") || isTrustedOAuthMetadata(nil, "x") || isTrustedOAuthMetadata([]any{}, "x") {
		t.Fatal("trusted a document without an issuer")
	}
}

// Go's net/url refuses a percent escape that is not two hex digits, which fetch would send as it is: the request
// is not made, as for any other URL fetch rejects (named limitation, port/lane-profile.md).
func TestProfileURLWithBrokenEscapeIsNotRequested(t *testing.T) {
	requests := 0
	hc := &http.Client{Transport: profRT(func(*http.Request) (*http.Response, error) {
		requests++
		return profTextResponse(404, ""), nil
	})}
	if got := resolveTokenEndpoint(context.Background(), "http://h.example/%zz", hc); got != "http://h.example/%zz/oauth/token" || requests != 0 {
		t.Fatalf("%q after %d requests", got, requests)
	}
	if _, err := profileFetch(context.Background(), hc, http.MethodGet, "http://h.example/%zz", nil, ""); err == nil {
		t.Fatal("expected an error")
	}
}

func TestProfileHeaderTypeErrors(t *testing.T) {
	for name, profile := range map[string]*omap{
		"access_token":  obj("oauth", obj("access_token", 5.0)),
		"refresh_token": obj("oauth", obj("refresh_token", []any{"x"})),
		"api_key":       obj("api_key", true),
	} {
		_, err1 := currentAuthHeaderFromProfile(profile)
		_, err2 := authHeaderFromProfile(profile)
		if name == "refresh_token" {
			// authHeaderFromProfile does not look at the refresh token.
			if !errors.Is(err1, errTrimNotFunction) || err2 != nil {
				t.Errorf("%s: %v %v", name, err1, err2)
			}
			continue
		}
		if !errors.Is(err1, errTrimNotFunction) || !errors.Is(err2, errTrimNotFunction) {
			t.Errorf("%s: %v %v", name, err1, err2)
		}
	}
	if p, err := newProfileAuth(&profileState{profile: obj("api_key", 1.0)}); p != nil || !errors.Is(err, errTrimNotFunction) {
		t.Fatalf("newProfileAuth: %v %v", p, err)
	}
}

func TestProfileRefreshGuards(t *testing.T) {
	// A profile without a refresh token is not refreshed even when asked to.
	p, err := newProfileAuth(&profileState{profile: obj("api_key", "K", "oauth", obj("access_token", "A")), config: newOmap()})
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	if err := p.refreshOAuthToken(nil); err != nil {
		t.Fatal(err)
	}
	// A profile that is not an object cannot take the token (a property assignment on a primitive throws).
	if applyTokenResponse(newOmap(), obj("access_token", "x")) {
		t.Fatal("applied the token to a profile without an oauth object")
	}
}

// release runs in `finally`: a failure to remove the lock directory rejects the refresh and so getAuthHeader.
func TestProfileLockReleaseFailureIsReturned(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a directory the user cannot empty")
	}
	ts := newTokenServer(t)
	ts.hold = make(chan struct{})
	path := profileEnv(t, oauthConfig(ts.URL, "OLD", "RT", expiredAt()))
	p := loadAuth(t)
	errc := make(chan error, 1)
	go func() {
		_, err := p.getAuthHeader(context.Background(), ts.Client())
		errc <- err
	}()
	<-ts.arrived
	dir := lockDirOf(path)
	if err := os.Chmod(dir, 0o500); err != nil { // entries cannot be unlinked
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	close(ts.hold)
	select {
	case err := <-errc:
		if err == nil || !errors.Is(err, os.ErrPermission) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no result")
	}
	// The token was still stored before the release failed.
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), `"AT-1"`) {
		t.Fatalf("config not rewritten:\n%s", b)
	}
	os.Chmod(dir, 0o700)
	os.RemoveAll(filepath.Clean(dir))
}
