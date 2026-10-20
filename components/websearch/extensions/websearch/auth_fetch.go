package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
)

// Port of auth-fetch.ts: the authFetch profiles of web-search.json, the host policy of an
// authenticated URL and the same-origin redirect guard, plus the per-hop cookie header that
// extract.ts resolveAuthCookieHeader builds from the browser cookies of chrome_cookies.go.

// AuthFetchProfile is one authFetch profile.
type AuthFetchProfile struct {
	Name          string
	Hosts         []string
	ChromeProfile string
	// Redirects is always "same-origin".
	Redirects string
	// Cache is "session" or "off".
	Cache string
}

var (
	authProfileNameRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
	authHostLabelsRE  = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	authHostForbidden = regexp.MustCompile("[\\s\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000\ufeff\\\\/?:#@*]")
)

// ResolveAuthFetchProfile is resolveAuthFetchProfile.
func ResolveAuthFetchProfile(request AuthSpec) (AuthFetchProfile, error) {
	profiles, err := loadAuthFetchProfiles()
	if err != nil {
		return AuthFetchProfile{}, err
	}
	if len(profiles) == 0 {
		return AuthFetchProfile{}, fmt.Errorf("auth requires at least one authFetch profile in %s", ConfigPath())
	}
	if request.True {
		if len(profiles) != 1 {
			return AuthFetchProfile{}, errors.New("auth: true requires exactly one authFetch profile; use a profile name instead")
		}
		return profiles[0], nil
	}
	name := jsTrim(request.Name)
	for _, p := range profiles {
		if p.Name == name {
			return p, nil
		}
	}
	return AuthFetchProfile{}, fmt.Errorf("Unknown authFetch profile: %s", name)
}

// originOf is URL.origin: the default port is dropped.
func originOf(u *url.URL) string {
	host := u.Host
	if port := u.Port(); (u.Scheme == "https" && port == "443") || (u.Scheme == "http" && port == "80") {
		host = strings.TrimSuffix(host, ":"+port)
	}
	return u.Scheme + "://" + host
}

func normalizeAuthHostname(h string) string {
	h = strings.ToLower(h)
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	return strings.TrimSuffix(h, ".")
}

func authHostMatches(hostname, allowed string) bool {
	return hostname == allowed || strings.HasSuffix(hostname, "."+allowed)
}

// AssertAuthFetchURL is assertAuthFetchUrl: an HTTPS URL on a host the profile allows.
func AssertAuthFetchURL(p AuthFetchProfile, rawURL string) (*url.URL, error) {
	u, err := ParseURL(rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("Authenticated fetch requires an HTTPS URL: %s", err.Error())
	}
	if u.Scheme != "https" {
		return nil, errors.New("Authenticated fetch requires an HTTPS URL")
	}
	hostname := normalizeAuthHostname(u.Hostname())
	for _, h := range p.Hosts {
		if authHostMatches(hostname, h) {
			return u, nil
		}
	}
	return nil, fmt.Errorf("URL host %s is not allowed by authFetch profile %s", hostname, p.Name)
}

// AuthFetchRedirectGuard is authFetchRedirectGuard.
func AuthFetchRedirectGuard(p AuthFetchProfile, from, to *url.URL) error {
	if p.Redirects == "same-origin" && originOf(to) != originOf(from) {
		return fmt.Errorf("Authenticated fetch refused cross-origin redirect: %s -> %s", originOf(from), originOf(to))
	}
	return nil
}

func loadAuthFetchProfiles() ([]AuthFetchProfile, error) {
	path := ConfigPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var root map[string]json.RawMessage
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("Failed to parse %s: %s", path, jsonErrorText(err))
	}
	if _, ok := generic.(map[string]any); !ok {
		return nil, fmt.Errorf("Failed to parse %s: expected a JSON object", path)
	}
	_ = json.Unmarshal(data, &root)
	raw, present := root["authFetch"]
	if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	var section any
	_ = json.Unmarshal(raw, &section)
	obj, ok := section.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("authFetch in %s must be an object", path)
	}
	var profiles []AuthFetchProfile
	for _, name := range objectKeyOrder(raw) {
		p, err := parseAuthProfile(name, obj[name])
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return profiles, nil
}

// objectKeyOrder lists the keys of a JSON object in document order (Object.entries upstream),
// each once, at the position of its last occurrence's first appearance.
func objectKeyOrder(raw json.RawMessage) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			break
		}
		k, _ := t.(string)
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			break
		}
	}
	return keys
}

func parseAuthProfile(name string, value any) (AuthFetchProfile, error) {
	path := ConfigPath()
	if !authProfileNameRE.MatchString(name) {
		return AuthFetchProfile{}, fmt.Errorf("authFetch profile name %s must start with a letter and contain only letters, numbers, underscores, or hyphens", jsonQuote(name))
	}
	if list, ok := value.([]any); ok {
		hosts, err := parseAuthHosts(list, "authFetch."+name)
		if err != nil {
			return AuthFetchProfile{}, err
		}
		return AuthFetchProfile{Name: name, Hosts: hosts, Redirects: "same-origin", Cache: "session"}, nil
	}
	config, ok := value.(map[string]any)
	if !ok {
		return AuthFetchProfile{}, fmt.Errorf("authFetch.%s in %s must be an array of hosts or an object", name, path)
	}
	hostList, ok := config["hosts"].([]any)
	if !ok {
		return AuthFetchProfile{}, fmt.Errorf("authFetch.%s.hosts in %s must be a non-empty array of hostnames", name, path)
	}
	if r := config["redirects"]; r != nil && r != "same-origin" {
		return AuthFetchProfile{}, fmt.Errorf(`authFetch.%s.redirects in %s must be "same-origin"`, name, path)
	}
	cache := "session"
	if c := config["cache"]; c != nil {
		if c != "session" && c != "off" {
			return AuthFetchProfile{}, fmt.Errorf(`authFetch.%s.cache in %s must be "session" or "off"`, name, path)
		}
		cache = c.(string)
	}
	chromeProfile, err := parseAuthChromeProfile(config["chromeProfile"], "authFetch."+name+".chromeProfile")
	if err != nil {
		return AuthFetchProfile{}, err
	}
	hosts, err := parseAuthHosts(hostList, "authFetch."+name+".hosts")
	if err != nil {
		return AuthFetchProfile{}, err
	}
	return AuthFetchProfile{Name: name, Hosts: hosts, ChromeProfile: chromeProfile, Redirects: "same-origin", Cache: cache}, nil
}

func jsonQuote(s string) string {
	b, _ := marshalNoEscape(s)
	return string(b)
}

func parseAuthHosts(list []any, label string) ([]string, error) {
	path := ConfigPath()
	if len(list) == 0 {
		return nil, fmt.Errorf("%s in %s must be a non-empty array of hostnames", label, path)
	}
	var hosts []string
	for _, entry := range list {
		s, ok := entry.(string)
		if !ok {
			return nil, fmt.Errorf("%s in %s must contain only hostnames", label, path)
		}
		h, err := parseAuthHost(s, label)
		if err != nil {
			return nil, err
		}
		if !sliceHas(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return hosts, nil
}

func parseAuthHost(value, label string) (string, error) {
	host := normalizeAuthHostname(jsTrim(value))
	invalid := fmt.Errorf("%s in %s contains an invalid hostname: %s", label, ConfigPath(), jsonQuote(value))
	if host == "" || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") || authHostForbidden.MatchString(host) {
		return "", invalid
	}
	if jsLen(host) > 253 || !authHostLabelsRE.MatchString(host) {
		return "", invalid
	}
	return host, nil
}

func parseAuthChromeProfile(value any, label string) (string, error) {
	if value == nil {
		return "", nil
	}
	s, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s in %s must be a string", label, ConfigPath())
	}
	n := jsTrim(s)
	if n == "" || n == "." || n == ".." || strings.Contains(n, "/") || strings.Contains(n, `\`) {
		return "", fmt.Errorf("%s in %s must be a profile directory name, not a path", label, ConfigPath())
	}
	return n, nil
}

// resolveAuthCookieHeader is extract.ts resolveAuthCookieHeader: the Cookie header of the
// profile's browser for one request URL.
func resolveAuthCookieHeader(ctx context.Context, u *url.URL, p AuthFetchProfile) (string, error) {
	parsed, err := AssertAuthFetchURL(p, u.String())
	if err != nil {
		return "", err
	}
	res, err := GetBrowserCookiesForHosts(ctx, BrowserCookieOptions{Hosts: []string{parsed.Hostname()}, Profile: p.ChromeProfile, RequestURL: parsed})
	if err != nil {
		return "", err
	}
	if res != nil && res.CookieHeader != "" {
		return res.CookieHeader, nil
	}
	if res == nil {
		msg := "Authenticated fetch profile " + p.Name + " could not read browser cookies"
		if d := LastBrowserCookieDiagnostic(); d != "" {
			msg += ": " + d
		}
		return "", errors.New(msg)
	}
	return "", fmt.Errorf("Authenticated fetch profile %s could not build a cookie header", p.Name)
}

// authFetchFunc is the transport of an authenticated fetch (fetchAuthenticatedRemoteUrl): each
// hop gets the cookies recomputed for its own URL, and a redirect that leaves the origin is
// refused before anything is sent. base is the page transport (nil: the guarded default).
func authFetchFunc(p AuthFetchProfile, base FetchFunc, vo ValidationOptions) FetchFunc {
	var previous *url.URL
	return func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error) {
		if previous != nil {
			if err := AuthFetchRedirectGuard(p, previous, u); err != nil {
				return nil, err
			}
		}
		cookie, err := resolveAuthCookieHeader(ctx, u, p)
		if err != nil {
			return nil, err
		}
		header := init.Header.Clone()
		if header == nil {
			header = http.Header{}
		}
		header.Set("Cookie", cookie)
		init.Header = header
		previous = u
		if base != nil {
			return base(ctx, u, init)
		}
		return guardedFetch(ctx, u, init, vo)
	}
}
