package websearch

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

// Twins of pi-web-access test/ssrf-protection.test.mjs (upstream 9a734ed).

func publicLookup(_ context.Context, _ string) ([]LookupAddress, error) {
	return []LookupAddress{{Address: "93.184.216.34", Family: 4}}, nil
}

func lookupOf(addrs ...LookupAddress) Lookup {
	return func(context.Context, string) ([]LookupAddress, error) { return addrs, nil }
}

func rejectsInternal(t *testing.T, raw string) {
	t.Helper()
	_, err := ValidateRemoteURL(bg(), raw, ValidationOptions{Lookup: publicLookup})
	if err == nil {
		t.Fatalf("%s should be blocked", raw)
	}
	wantErr(t, err, `internal|Blocked`)
}

func stubResponse(status int, location, body string) *http.Response {
	h := http.Header{}
	if location != "" {
		h.Set("Location", location)
	}
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func hostnameOf(u *url.URL) string { return JSHostname(u) }

func TestUpstream_ssrf_protection(t *testing.T) {
	const f = "ssrf-protection"

	tw(t, f, "validateRemoteUrl blocks localhost, loopback, link-local, private, and metadata targets", func(t *testing.T) {
		for _, u := range []string{
			"http://localhost/", "http://127.0.0.1/", "http://10.0.0.1/", "http://172.16.0.1/", "http://192.168.1.1/",
			"http://169.254.169.254/latest/meta-data/", "http://0.0.0.0/", "http://[::1]/", "http://[fe80::1]/",
			"http://[fd00::1]/", "http://[::ffff:127.0.0.1]/",
		} {
			rejectsInternal(t, u)
		}
	})

	tw(t, f, "validateRemoteUrl blocks encoded and alternate loopback IPv4 forms", func(t *testing.T) {
		for _, u := range []string{"http://2130706433/", "http://0177.0.0.1/", "http://0x7f.0.0.1/", "http://127.1/"} {
			rejectsInternal(t, u)
		}
	})

	tw(t, f, "validateRemoteUrl blocks hostnames that resolve to private addresses", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: lookupOf(LookupAddress{"192.168.0.2", 4})})
		wantErr(t, err, `Blocked internal address for example\.test: 192\.168\.0\.2`)
		_, err = ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: lookupOf(LookupAddress{"93.184.216.34", 4}, LookupAddress{"fd00::1", 6})})
		wantErr(t, err, `Blocked internal address for example\.test: fd00::1`)
	})

	tw(t, f, "loopback opt-in does not allow arbitrary hostnames that resolve to loopback", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{AllowLoopback: true, Lookup: lookupOf(LookupAddress{"127.0.0.1", 4})})
		wantErr(t, err, `Blocked internal address for example\.test: 127\.0\.0\.1`)
	})

	tw(t, f, "validateRemoteUrl permits public HTTP and HTTPS targets", func(t *testing.T) {
		u, err := ValidateRemoteURL(bg(), "https://example.com/path", ValidationOptions{Lookup: publicLookup})
		noErr(t, err)
		if hostnameOf(u) != "example.com" {
			t.Fatal(hostnameOf(u))
		}
		u, err = ValidateRemoteURL(bg(), "http://93.184.216.34/", ValidationOptions{})
		noErr(t, err)
		if hostnameOf(u) != "93.184.216.34" {
			t.Fatal(hostnameOf(u))
		}
		u, err = ValidateRemoteURL(bg(), "https://[2606:2800:220:1:248:1893:25c8:1946]/", ValidationOptions{})
		noErr(t, err)
		if hostnameOf(u) != "[2606:2800:220:1:248:1893:25c8:1946]" {
			t.Fatal(hostnameOf(u))
		}
	})

	tw(t, f, "domain policy allows exact and subdomain matches, and is off by default", func(t *testing.T) {
		u, err := ValidateRemoteURL(bg(), "https://example.com/", ValidationOptions{Lookup: publicLookup})
		noErr(t, err)
		if hostnameOf(u) != "example.com" {
			t.Fatal()
		}
		u, err = ValidateRemoteURL(bg(), "https://www.example.com/", ValidationOptions{Lookup: publicLookup, DomainPolicy: &DomainPolicy{Allow: []string{"example.com"}}})
		noErr(t, err)
		if hostnameOf(u) != "www.example.com" {
			t.Fatal()
		}
		_, err = ValidateRemoteURL(bg(), "https://other.test/", ValidationOptions{Lookup: publicLookup, DomainPolicy: &DomainPolicy{Allow: []string{"example.com"}}})
		wantErr(t, err, `Hostname not allowed by fetch_content domain policy`)
	})

	tw(t, f, "domain policy deny wins over allow", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "https://private.example.com/", ValidationOptions{Lookup: publicLookup,
			DomainPolicy: &DomainPolicy{Allow: []string{"example.com"}, Deny: []string{"private.example.com"}}})
		wantErr(t, err, `Blocked hostname by fetch_content domain policy`)
	})

	tw(t, f, "domain policy validates redirect targets before following", func(t *testing.T) {
		var requested []string
		fetch := func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			requested = append(requested, u.String())
			return stubResponse(302, "https://denied.example/next", ""), nil
		}
		_, err := FetchRemoteURL(bg(), "https://allowed.example/", RequestInit{}, FetchRemoteOptions{
			ValidationOptions: ValidationOptions{Lookup: publicLookup, DomainPolicy: &DomainPolicy{Allow: []string{"allowed.example"}, Deny: []string{"denied.example"}}},
			Fetch:             fetch,
		})
		wantErr(t, err, `Blocked hostname by fetch_content domain policy`)
		if !reflect.DeepEqual(requested, []string{"https://allowed.example/"}) {
			t.Fatalf("%v", requested)
		}
	})

	tw(t, f, "domain policy never relaxes SSRF protection", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "http://127.0.0.1/", ValidationOptions{DomainPolicy: &DomainPolicy{Allow: []string{"127.0.0.1"}}})
		wantErr(t, err, `Blocked internal address`)
		_, err = ValidateRemoteURL(bg(), "https://internal.example/", ValidationOptions{Lookup: lookupOf(LookupAddress{"10.0.0.5", 4}), DomainPolicy: &DomainPolicy{Allow: []string{"example"}}})
		wantErr(t, err, `Blocked internal address`)
	})

	tw(t, f, "allowLoopback exempts only the configured origin, not redirect targets", func(t *testing.T) {
		var requested []string
		redirecting := func(location string) FetchFunc {
			return func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
				requested = append(requested, u.String())
				if len(requested) == 1 {
					return stubResponse(302, location, ""), nil
				}
				return stubResponse(200, "", "ok"), nil
			}
		}
		res, err := FetchRemoteURL(bg(), "http://127.0.0.1:11235/md", RequestInit{}, FetchRemoteOptions{
			ValidationOptions: ValidationOptions{Lookup: publicLookup, AllowLoopback: true},
			Fetch:             redirecting("http://127.0.0.1:11235/api/md"),
		})
		noErr(t, err)
		if res.StatusCode != 200 || !reflect.DeepEqual(requested, []string{"http://127.0.0.1:11235/md", "http://127.0.0.1:11235/api/md"}) {
			t.Fatalf("%d %v", res.StatusCode, requested)
		}
		requested = nil
		_, err = FetchRemoteURL(bg(), "http://127.0.0.1:11235/md", RequestInit{}, FetchRemoteOptions{
			ValidationOptions: ValidationOptions{Lookup: publicLookup, AllowLoopback: true},
			Fetch:             redirecting("http://127.0.0.2:9999/private"),
		})
		wantErr(t, err, `Blocked internal address`)
		if !reflect.DeepEqual(requested, []string{"http://127.0.0.1:11235/md"}) {
			t.Fatalf("%v", requested)
		}
		requested = nil
		_, err = FetchRemoteURL(bg(), "http://127.0.0.1:11235/md", RequestInit{}, FetchRemoteOptions{
			ValidationOptions: ValidationOptions{Lookup: publicLookup, AllowLoopback: true},
			Fetch:             redirecting("http://localhost:11235/md"),
		})
		wantErr(t, err, `Blocked internal hostname`)
		if !reflect.DeepEqual(requested, []string{"http://127.0.0.1:11235/md"}) {
			t.Fatalf("%v", requested)
		}
	})

	tw(t, f, "fetchRemoteUrl validates redirect targets before following", func(t *testing.T) {
		var requested []string
		fetch := func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			requested = append(requested, u.String())
			return stubResponse(302, "http://127.0.0.1/admin", ""), nil
		}
		_, err := FetchRemoteURL(bg(), "https://example.com/", RequestInit{}, FetchRemoteOptions{ValidationOptions: ValidationOptions{Lookup: publicLookup}, Fetch: fetch})
		wantErr(t, err, `Blocked internal address`)
		if !reflect.DeepEqual(requested, []string{"https://example.com/"}) {
			t.Fatalf("%v", requested)
		}
	})

	tw(t, f, "fetchRemoteUrl follows validated public redirects manually", func(t *testing.T) {
		var requested []string
		fetch := func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			requested = append(requested, u.String())
			if len(requested) == 1 {
				return stubResponse(301, "/next", ""), nil
			}
			return stubResponse(200, "", "ok"), nil
		}
		res, err := FetchRemoteURL(bg(), "https://example.com/start", RequestInit{}, FetchRemoteOptions{ValidationOptions: ValidationOptions{Lookup: publicLookup}, Fetch: fetch})
		noErr(t, err)
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 || string(body) != "ok" || !reflect.DeepEqual(requested, []string{"https://example.com/start", "https://example.com/next"}) {
			t.Fatalf("%d %q %v", res.StatusCode, body, requested)
		}
	})

	tw(t, f, "fake-IP block errors point to the allowRanges opt-in", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: lookupOf(LookupAddress{"198.18.0.56", 4})})
		wantErr(t, err, `Blocked internal address for example\.test: 198\.18\.0\.56\..*TUN/fake-IP proxies.*ssrf\.allowRanges.*198\.18\.0\.0/15`)
	})

	tw(t, f, "allowRanges exempts a synthetic fake-IP range (e.g. 198.18.0.0/15)", func(t *testing.T) {
		fake := lookupOf(LookupAddress{"198.18.0.56", 4})
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: fake})
		wantErr(t, err, `Blocked internal address for example\.test: 198\.18\.0\.56`)
		u, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: fake, AllowRanges: []string{"198.18.0.0/15"}})
		noErr(t, err)
		if hostnameOf(u) != "example.test" {
			t.Fatal()
		}
		u, err = ValidateRemoteURL(bg(), "http://198.18.0.99/", ValidationOptions{AllowRanges: []string{"198.18.0.0/15"}})
		noErr(t, err)
		if hostnameOf(u) != "198.18.0.99" {
			t.Fatal()
		}
	})

	tw(t, f, "allowRanges works for IPv6 ranges", func(t *testing.T) {
		fake := lookupOf(LookupAddress{"fd00::1", 6})
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: fake})
		wantErr(t, err, `Blocked internal address for example\.test: fd00::1`)
		u, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: fake, AllowRanges: []string{"fd00::/8"}})
		noErr(t, err)
		if hostnameOf(u) != "example.test" {
			t.Fatal()
		}
	})

	tw(t, f, "allowRanges does not relax protection outside the listed range", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "https://example.test/", ValidationOptions{Lookup: lookupOf(LookupAddress{"10.0.0.1", 4}), AllowRanges: []string{"198.18.0.0/15"}})
		wantErr(t, err, `Blocked internal address for example\.test: 10\.0\.0\.1`)
		for _, host := range []string{"198.18.0.0", "198.18.0.1"} {
			u, err := ValidateRemoteURL(bg(), "http://"+host+"/", ValidationOptions{AllowRanges: []string{"198.18.0.0/31"}})
			noErr(t, err)
			if hostnameOf(u) != host {
				t.Fatal()
			}
		}
		_, err = ValidateRemoteURL(bg(), "http://198.18.0.2/", ValidationOptions{AllowRanges: []string{"198.18.0.0/31"}})
		wantErr(t, err, `Blocked internal address`)
	})

	tw(t, f, "allowRanges accepts a bare host (no prefix) and treats it as /32", func(t *testing.T) {
		u, err := ValidateRemoteURL(bg(), "http://198.18.1.2/", ValidationOptions{AllowRanges: []string{"198.18.1.2"}})
		noErr(t, err)
		if hostnameOf(u) != "198.18.1.2" {
			t.Fatal()
		}
	})

	tw(t, f, "allowRanges rejects an empty or non-numeric CIDR prefix instead of treating it as /0", func(t *testing.T) {
		for _, bad := range []string{"198.18.0.0/", "198.18.0.0/ ", "fd00::/", "10.0.0.0/abc", "10.0.0.0/ 8"} {
			_, err := ValidateRemoteURL(bg(), "http://198.18.0.5/", ValidationOptions{AllowRanges: []string{bad}})
			wantErr(t, err, `Invalid CIDR notation in ssrf\.allowRanges`)
		}
		_, err := ValidateRemoteURL(bg(), "http://169.254.169.254/", ValidationOptions{AllowRanges: []string{"198.18.0.0/"}})
		wantErr(t, err, `Invalid CIDR notation in ssrf\.allowRanges`)
	})

	tw(t, f, "allowRanges rejects all-address /0 CIDRs", func(t *testing.T) {
		_, err := ValidateRemoteURL(bg(), "http://169.254.169.254/", ValidationOptions{AllowRanges: []string{"0.0.0.0/0"}})
		wantErr(t, err, `Invalid CIDR notation in ssrf\.allowRanges`)
		_, err = ValidateRemoteURL(bg(), "http://[fd00::1]/", ValidationOptions{AllowRanges: []string{"::/0"}})
		wantErr(t, err, `Invalid CIDR notation in ssrf\.allowRanges`)
	})

	tw(t, f, "invalid allowRanges entries throw a descriptive error", func(t *testing.T) {
		for _, bad := range []string{"not-an-ip", "198.18.0.0/33", "198.18.0.0/-1", "999.0.0.0/8", "fd00::/129"} {
			_, err := ValidateRemoteURL(bg(), "http://198.18.0.5/", ValidationOptions{AllowRanges: []string{bad}})
			if err == nil || !strings.Contains(err.Error(), `Invalid CIDR notation in ssrf.allowRanges: "`+bad+`"`) {
				t.Fatalf("%s: %v", bad, err)
			}
		}
		// A non-array value cannot be expressed as []string; the config loader rejects it before validation
		// (see loadSsrfConfig twins). ParseAllowRanges takes the raw JSON value like the original.
		_, err := ParseAllowRanges("198.18.0.0/15")
		wantErr(t, err, `ssrf\.allowRanges must be an array`)
	})

	tw(t, f, "allowRanges flows through fetchRemoteUrl and its redirect targets", func(t *testing.T) {
		var requested []string
		fetch := func(_ context.Context, u *url.URL, _ RequestInit) (*http.Response, error) {
			requested = append(requested, u.String())
			if len(requested) == 1 {
				return stubResponse(302, "http://198.18.0.99/admin", ""), nil
			}
			return stubResponse(200, "", "ok"), nil
		}
		res, err := FetchRemoteURL(bg(), "https://example.com/", RequestInit{}, FetchRemoteOptions{
			ValidationOptions: ValidationOptions{Lookup: publicLookup, AllowRanges: []string{"198.18.0.0/15"}}, Fetch: fetch})
		noErr(t, err)
		if res.StatusCode != 200 || !reflect.DeepEqual(requested, []string{"https://example.com/", "http://198.18.0.99/admin"}) {
			t.Fatalf("%v", requested)
		}
	})

	clearProxyEnv := func(t *testing.T) {
		unsetenv(t, "HTTP_PROXY", "http_proxy", "HTTPS_PROXY", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy")
	}

	tw(t, f, "trustEnvProxy skips hostname DNS only for a configured proxy", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example.test:8080")
		lookups := 0
		lookup := func(context.Context, string) ([]LookupAddress, error) {
			lookups++
			return nil, errors.New("DNS should not run for proxied host")
		}
		_, err := ValidateRemoteURL(bg(), "https://public.example.test/", ValidationOptions{TrustEnvProxy: true, Lookup: lookup})
		noErr(t, err)
		if lookups != 0 {
			t.Fatal(lookups)
		}
		_, err = ValidateRemoteURL(bg(), "https://127.0.0.1/", ValidationOptions{TrustEnvProxy: true, Lookup: lookup})
		wantErr(t, err, `Blocked internal address`)
		_, err = ValidateRemoteURL(bg(), "https://localhost/", ValidationOptions{TrustEnvProxy: true, Lookup: lookup})
		wantErr(t, err, `Blocked internal hostname`)
	})

	tw(t, f, "trustEnvProxy still validates DNS when a curl proxy is active", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://env-proxy.example.test:8080")
		lookups := 0
		ctx := WithProxy(bg(), "http://curl-proxy.example.test:8080")
		_, err := ValidateRemoteURL(ctx, "https://public.example.test/", ValidationOptions{TrustEnvProxy: true, Lookup: func(context.Context, string) ([]LookupAddress, error) {
			lookups++
			return []LookupAddress{{"10.0.0.10", 4}}, nil
		}})
		wantErr(t, err, `Blocked internal address`)
		if lookups != 1 {
			t.Fatal(lookups)
		}
	})

	tw(t, f, "trustEnvProxy still validates DNS when empty proxy forces direct access", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://env-proxy.example.test:8080")
		lookups := 0
		ctx := WithProxy(bg(), "")
		_, err := ValidateRemoteURL(ctx, "https://public.example.test/", ValidationOptions{TrustEnvProxy: true, Lookup: func(context.Context, string) ([]LookupAddress, error) {
			lookups++
			return []LookupAddress{{"10.0.0.10", 4}}, nil
		}})
		wantErr(t, err, `Blocked internal address`)
		if lookups != 1 {
			t.Fatal(lookups)
		}
	})

	tw(t, f, "trustEnvProxy ignores invalid proxy environment values", func(t *testing.T) {
		clearProxyEnv(t)
		for _, value := range []string{"garbage", "file:///tmp/proxy", "   "} {
			t.Setenv("HTTPS_PROXY", value)
			lookups := 0
			u, err := ValidateRemoteURL(bg(), "https://public.example.test/", ValidationOptions{TrustEnvProxy: true, Lookup: func(context.Context, string) ([]LookupAddress, error) {
				lookups++
				return []LookupAddress{{"93.184.216.34", 4}}, nil
			}})
			noErr(t, err)
			if hostnameOf(u) != "public.example.test" || lookups != 1 {
				t.Fatalf("%s %d", value, lookups)
			}
		}
	})

	tw(t, f, "trustEnvProxy still performs DNS for NO_PROXY hosts", func(t *testing.T) {
		clearProxyEnv(t)
		t.Setenv("HTTPS_PROXY", "http://proxy.example.test:8080")
		t.Setenv("NO_PROXY", "public.example.test:443, .internal.example.test")
		lookups := 0
		lookup := func(context.Context, string) ([]LookupAddress, error) {
			lookups++
			return []LookupAddress{{"93.184.216.34", 4}}, nil
		}
		for _, c := range []struct {
			url  string
			want int
		}{
			{"https://public.example.test/", 1},
			{"https://api.internal.example.test/", 2},
			{"https://public.example.test:8443/", 2},
			{"https://other.example.test/", 2},
		} {
			_, err := ValidateRemoteURL(bg(), c.url, ValidationOptions{TrustEnvProxy: true, Lookup: lookup})
			noErr(t, err)
			if lookups != c.want {
				t.Fatalf("%s: lookups %d want %d", c.url, lookups, c.want)
			}
		}
	})
}
