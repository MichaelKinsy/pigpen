package websearch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Port of ssrf-protection.ts (pi-web-access 0.33.0). Every remote fetch is validated before
// it is sent and again on each redirect; the transport (transport.go) re-checks the resolved
// address at dial time, which closes the DNS-rebinding window the original leaves open.

const defaultMaxRedirects = 5

var redirectStatuses = map[int]bool{301: true, 302: true, 303: true, 307: true, 308: true}

var loopbackAllowRanges = []string{"127.0.0.0/8", "::1", "::ffff:127.0.0.0/104"}

// LookupAddress is one resolved address.
type LookupAddress struct {
	Address string
	Family  int
}

// Lookup resolves a hostname (a test seam; the default uses the system resolver).
type Lookup func(ctx context.Context, hostname string) ([]LookupAddress, error)

// DomainPolicy is fetchContent.domainPolicy: deny wins, an allow list makes everything else illegal.
type DomainPolicy struct{ Allow, Deny []string }

// SsrfConfig is the `ssrf` object of web-search.json.
type SsrfConfig struct {
	AllowRanges   []string
	TrustEnvProxy bool
}

// ValidationOptions mirror the original's ValidationOptions.
type ValidationOptions struct {
	Lookup       Lookup
	DomainPolicy *DomainPolicy
	// AllowRanges exempts CIDR ranges (TUN/fake-IP proxies); invalid entries are errors.
	AllowRanges []string
	// TrustEnvProxy skips local DNS for hostnames when an environment proxy is configured.
	TrustEnvProxy bool
	// AllowLoopback exempts an explicitly configured provider endpoint, never a redirect target.
	AllowLoopback bool
}

// RequestInit is the mutable part of a request that redirects rewrite.
type RequestInit struct {
	Method string
	Header http.Header
	Body   []byte
}

// FetchFunc performs one request without following redirects.
type FetchFunc func(ctx context.Context, u *url.URL, init RequestInit) (*http.Response, error)

// RedirectArgs is passed to OnRedirect.
type RedirectArgs struct {
	From, To *url.URL
	Init     RequestInit
	Response *http.Response
}

// FetchRemoteOptions add redirect handling to ValidationOptions.
type FetchRemoteOptions struct {
	ValidationOptions
	Fetch FetchFunc
	// MaxRedirects <= 0 means the default of 5.
	MaxRedirects int
	OnRedirect   func(RedirectArgs) RequestInit
}

func defaultLookup(ctx context.Context, hostname string) ([]LookupAddress, error) {
	addrs, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return nil, err
	}
	out := make([]LookupAddress, 0, len(addrs))
	for _, a := range addrs {
		fam := 6
		if a.IP.To4() != nil {
			fam = 4
		}
		out = append(out, LookupAddress{Address: a.IP.String(), Family: fam})
	}
	return out, nil
}

// ValidateRemoteURL rejects non-HTTP(S) URLs, internal hostnames and addresses, hostnames that
// resolve to internal addresses, and URLs the domain policy forbids.
func ValidateRemoteURL(ctx context.Context, raw string, o ValidationOptions) (*url.URL, error) {
	u, err := ParseURL(raw, nil)
	if err != nil {
		return nil, err
	}
	return validateParsedRemoteURL(ctx, u, o)
}

func validateParsedRemoteURL(ctx context.Context, u *url.URL, o ValidationOptions) (*url.URL, error) {
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("Only HTTP and HTTPS URLs can be fetched remotely")
	}
	hostname := normalizeHostname(u.Hostname())
	if hostname == "" {
		return nil, errors.New("URL must include a hostname")
	}
	if hostname == "localhost" {
		if o.AllowLoopback {
			return u, nil
		}
		return nil, fmt.Errorf("Blocked internal hostname: %s", hostname)
	}
	if strings.HasSuffix(hostname, ".localhost") {
		return nil, fmt.Errorf("Blocked internal hostname: %s", hostname)
	}
	allowRanges, err := ParseAllowRanges(o.AllowRanges)
	if err != nil {
		return nil, err
	}
	if err := assertDomainPolicy(hostname, o.DomainPolicy); err != nil {
		return nil, err
	}
	if ipVersion(hostname) != 0 {
		ranges := allowRanges
		if o.AllowLoopback {
			loop, _ := ParseAllowRanges(loopbackAllowRanges)
			ranges = append(append([]CIDR{}, allowRanges...), loop...)
		}
		if err := assertPublicAddress(hostname, hostname, ranges); err != nil {
			return nil, err
		}
		return u, nil
	}
	if shouldTrustEnvProxy(ctx, u, o.TrustEnvProxy) {
		return u, nil
	}
	lookup := o.Lookup
	if lookup == nil {
		lookup = defaultLookup
	}
	addrs, err := lookup(ctx, hostname)
	if err != nil {
		return nil, fmt.Errorf("Failed to resolve %s: %s", hostname, err.Error())
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("Failed to resolve %s: no addresses returned", hostname)
	}
	for _, a := range addrs {
		if err := assertPublicAddress(a.Address, hostname, allowRanges); err != nil {
			return nil, err
		}
	}
	return u, nil
}

// FetchRemoteURL fetches raw, validating the URL and every redirect target before it is sent.
// Redirects are followed manually: 303, and 301/302 after POST, become GET without a body.
func FetchRemoteURL(ctx context.Context, raw string, init RequestInit, o FetchRemoteOptions) (*http.Response, error) {
	maxRedirects := o.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	current, err := ValidateRemoteURL(ctx, raw, o.ValidationOptions)
	if err != nil {
		return nil, err
	}
	configuredOrigin := Origin(current)
	hopOptions := o.ValidationOptions
	requestInit := init
	for redirects := 0; redirects <= maxRedirects; redirects++ {
		var resp *http.Response
		if o.Fetch != nil {
			resp, err = o.Fetch(ctx, current, requestInit)
		} else {
			resp, err = guardedFetch(ctx, current, requestInit, hopOptions)
		}
		if err != nil {
			return nil, err
		}
		if !redirectStatuses[resp.StatusCode] {
			return resp, nil
		}
		location := resp.Header.Get("Location")
		if location == "" {
			return resp, nil
		}
		if redirects == maxRedirects {
			_ = resp.Body.Close()
			return nil, fmt.Errorf("Too many redirects fetching %s", current.String())
		}
		_ = resp.Body.Close()
		from := current
		next, err := ParseURL(location, current)
		if err != nil {
			return nil, err
		}
		// allowLoopback exempts an explicitly configured endpoint, never a redirect target: a
		// loopback base must not be able to pivot the request onto a different loopback origin.
		hopOptions = o.ValidationOptions
		if Origin(next) != configuredOrigin {
			hopOptions.AllowLoopback = false
		}
		current, err = validateParsedRemoteURL(ctx, next, hopOptions)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == 303 || ((resp.StatusCode == 301 || resp.StatusCode == 302) && strings.EqualFold(requestInit.Method, "POST")) {
			requestInit.Body = nil
			requestInit.Method = "GET"
		}
		if o.OnRedirect != nil {
			requestInit = o.OnRedirect(RedirectArgs{From: from, To: current, Init: requestInit, Response: resp})
		}
	}
	return nil, fmt.Errorf("Too many redirects fetching %s", current.String())
}

func assertDomainPolicy(hostname string, policy *DomainPolicy) error {
	if policy == nil {
		return nil
	}
	for _, e := range policy.Deny {
		if domainMatches(hostname, e) {
			return fmt.Errorf("Blocked hostname by fetch_content domain policy: %s", hostname)
		}
	}
	if len(policy.Allow) > 0 {
		for _, e := range policy.Allow {
			if domainMatches(hostname, e) {
				return nil
			}
		}
		return fmt.Errorf("Hostname not allowed by fetch_content domain policy: %s", hostname)
	}
	return nil
}

func domainMatches(hostname, entry string) bool {
	return hostname == entry || strings.HasSuffix(hostname, "."+entry)
}

func proxyForProtocol(scheme string) string {
	var names []string
	switch scheme {
	case "http":
		names = []string{"HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}
	case "https":
		names = []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"}
	}
	for _, name := range names {
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			continue
		}
		if p, err := url.Parse(value); err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Hostname() != "" {
			return value
		}
	}
	return ""
}

var digitsOnly = regexp.MustCompile(`^\d+$`)

func hostnameMatchesNoProxy(hostname, port, entry string) bool {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return false
	}
	if trimmed == "*" {
		return true
	}
	hostEntry, entryPort := trimmed, ""
	if strings.HasPrefix(hostEntry, "[") {
		if closing := strings.Index(hostEntry, "]"); closing >= 0 {
			suffix := hostEntry[closing+1:]
			// Upstream's regexp here is /^:\\d+$/ (an escaped backslash), so a port on a bracketed
			// literal never matches; the behaviour is kept.
			_ = suffix
			hostEntry = hostEntry[:closing+1]
		}
	} else if colon := strings.LastIndex(hostEntry, ":"); colon > -1 && digitsOnly.MatchString(hostEntry[colon+1:]) {
		entryPort = hostEntry[colon+1:]
		hostEntry = hostEntry[:colon]
	}
	if entryPort != "" && entryPort != port {
		return false
	}
	normalized := normalizeHostname(hostEntry)
	if normalized == "" {
		return false
	}
	if normalized == hostname {
		return true
	}
	var suffix string
	switch {
	case strings.HasPrefix(normalized, "*."):
		suffix = normalized[1:]
	case strings.HasPrefix(normalized, "."):
		suffix = normalized
	default:
		suffix = "." + normalized
	}
	return strings.HasSuffix(hostname, suffix)
}

func envNoProxy() string {
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

func shouldTrustEnvProxy(ctx context.Context, u *url.URL, enabled bool) bool {
	if !enabled || proxyForProtocol(u.Scheme) == "" {
		return false
	}
	if hasScopedProxyDecision(ctx) {
		return false
	}
	hostname := normalizeHostname(u.Hostname())
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	for _, entry := range strings.Split(envNoProxy(), ",") {
		if hostnameMatchesNoProxy(hostname, port, entry) {
			return false
		}
	}
	return true
}

func assertPublicAddress(address, hostname string, allowRanges []CIDR) error {
	normalized := normalizeHostname(address)
	version := ipVersion(normalized)
	if version == 0 {
		return fmt.Errorf("Resolved non-IP address for %s: %s", hostname, address)
	}
	// Explicitly-allowed ranges bypass the checks below. This lets users exempt synthetic
	// ranges produced by TUN/fake-IP proxies (e.g. 198.18/15).
	if isInAllowedRange(normalized, version, allowRanges) {
		return nil
	}
	if version == 4 && isBlockedIPv4(normalized) {
		hint := ""
		if isFakeIPProxyAddress(normalized) {
			hint = `. This address is in 198.18.0.0/15, commonly used by TUN/fake-IP proxies. If that matches your setup, configure ssrf.allowRanges with ["198.18.0.0/15"] in web-search.json.`
		}
		return fmt.Errorf("Blocked internal address for %s: %s%s", hostname, normalized, hint)
	}
	if version == 6 && isBlockedIPv6(normalized) {
		return fmt.Errorf("Blocked internal address for %s: %s", hostname, normalized)
	}
	return nil
}

func isFakeIPProxyAddress(address string) bool {
	parts := strings.Split(address, ".")
	if len(parts) < 2 {
		return false
	}
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return a == 198 && (b == 18 || b == 19)
}

func isBlockedIPv4(address string) bool {
	parts := strings.Split(address, ".")
	if len(parts) != 4 {
		return true
	}
	n := make([]int, 4)
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || v > 255 {
			return true
		}
		n[i] = v
	}
	a, b := n[0], n[1]
	return a == 0 || a == 10 || a == 127 ||
		(a == 100 && b >= 64 && b <= 127) ||
		(a == 169 && b == 254) ||
		(a == 172 && b >= 16 && b <= 31) ||
		(a == 192 && b == 168) ||
		isFakeIPProxyAddress(address) ||
		a >= 224
}

func ipv6Groups(address string) ([8]int, bool) {
	var groups [8]int
	addr, err := netip.ParseAddr(address)
	if err != nil || !addr.Is6() || addr.Zone() != "" {
		return groups, false
	}
	b := addr.As16()
	for i := 0; i < 8; i++ {
		groups[i] = int(b[i*2])<<8 | int(b[i*2+1])
	}
	return groups, true
}

func isBlockedIPv6(address string) bool {
	groups, ok := ipv6Groups(address)
	if !ok {
		return true
	}
	first := groups[0]
	allZero := true
	for _, g := range groups {
		if g != 0 {
			allZero = false
		}
	}
	if allZero {
		return true
	}
	zeroPrefix7 := true
	for _, g := range groups[:7] {
		if g != 0 {
			zeroPrefix7 = false
		}
	}
	if zeroPrefix7 && groups[7] == 1 {
		return true
	}
	if first&0xfe00 == 0xfc00 || first&0xffc0 == 0xfe80 {
		return true
	}
	v4 := func(hi, lo int) string { return fmt.Sprintf("%d.%d.%d.%d", hi>>8, hi&0xff, lo>>8, lo&0xff) }
	zero5 := true
	for _, g := range groups[:5] {
		if g != 0 {
			zero5 = false
		}
	}
	if zero5 && groups[5] == 0xffff {
		return isBlockedIPv4(v4(groups[6], groups[7]))
	}
	// Hardening beyond the original (listed in PORT.md): multicast, and IPv4 embedded in the
	// NAT64 (64:ff9b::/96) and 6to4 (2002::/16) forms, which would reach an internal IPv4 target.
	if first&0xff00 == 0xff00 {
		return true
	}
	if first == 0x64 && groups[1] == 0xff9b && groups[2] == 0 && groups[3] == 0 && groups[4] == 0 && groups[5] == 0 {
		return isBlockedIPv4(v4(groups[6], groups[7]))
	}
	if first == 0x2002 {
		return isBlockedIPv4(v4(groups[1], groups[2]))
	}
	return false
}

// CIDR is a parsed allow range: a network address (4 or 16 bytes) and prefix length.
type CIDR struct {
	Bytes  []byte
	Prefix int
}

// ParseAllowRanges validates the `allowRanges` value (a []string, []any or a raw JSON value).
// Entries are CIDR blocks or bare hosts; /0 and malformed prefixes are rejected so a typo
// cannot exempt every address.
func ParseAllowRanges(input any) ([]CIDR, error) {
	if input == nil {
		return nil, nil
	}
	var entries []any
	switch v := input.(type) {
	case []string:
		for _, s := range v {
			entries = append(entries, s)
		}
	case []any:
		entries = v
	default:
		return nil, errors.New("ssrf.allowRanges must be an array of CIDR strings")
	}
	var rules []CIDR
	for _, e := range entries {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("ssrf.allowRanges entries must be strings, got %s", jsType(e))
		}
		rule, ok := parseCIDR(strings.TrimSpace(s))
		if !ok {
			return nil, fmt.Errorf("Invalid CIDR notation in ssrf.allowRanges: %q", s)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

func parseCIDR(raw string) (CIDR, bool) {
	if raw == "" {
		return CIDR{}, false
	}
	slash := strings.LastIndex(raw, "/")
	addrPart, prefixPart, hasPrefix := raw, "", false
	if slash >= 0 {
		addrPart, prefixPart, hasPrefix = raw[:slash], raw[slash+1:], true
	}
	// A slash must be followed by digits: "198.18.0.0/" must not become /0.
	if hasPrefix && !digitsOnly.MatchString(prefixPart) {
		return CIDR{}, false
	}
	switch ipVersion(addrPart) {
	case 4:
		addr, err := netip.ParseAddr(addrPart)
		if err != nil {
			return CIDR{}, false
		}
		prefix := 32
		if hasPrefix {
			prefix, _ = strconv.Atoi(prefixPart)
		}
		if prefix < 1 || prefix > 32 {
			return CIDR{}, false
		}
		b := addr.As4()
		return CIDR{Bytes: b[:], Prefix: prefix}, true
	case 6:
		addr, err := netip.ParseAddr(addrPart)
		if err != nil {
			return CIDR{}, false
		}
		prefix := 128
		if hasPrefix {
			prefix, _ = strconv.Atoi(prefixPart)
		}
		if prefix < 1 || prefix > 128 {
			return CIDR{}, false
		}
		b := addr.As16()
		return CIDR{Bytes: b[:], Prefix: prefix}, true
	}
	return CIDR{}, false
}

func isInAllowedRange(address string, version int, ranges []CIDR) bool {
	if len(ranges) == 0 {
		return false
	}
	addr, err := netip.ParseAddr(address)
	if err != nil {
		return false
	}
	var bytes []byte
	if version == 4 {
		b := addr.As4()
		bytes = b[:]
	} else {
		b := addr.As16()
		bytes = b[:]
	}
	for _, r := range ranges {
		if len(r.Bytes) != len(bytes) {
			continue
		}
		if bytesMatchPrefix(bytes, r.Bytes, r.Prefix) {
			return true
		}
	}
	return false
}

func bytesMatchPrefix(addr, network []byte, prefix int) bool {
	full, rem := prefix>>3, prefix&7
	for i := 0; i < full; i++ {
		if addr[i] != network[i] {
			return false
		}
	}
	if rem > 0 && full < len(addr) {
		mask := byte(0xff << (8 - rem))
		if addr[full]&mask != network[full]&mask {
			return false
		}
	}
	return true
}

// ── configuration ──

// LoadSsrfConfig reads the `ssrf` object of web-search.json.
func LoadSsrfConfig() (SsrfConfig, error) {
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return SsrfConfig{}, err
	}
	path := ConfigPath()
	raw, present := root["ssrf"]
	if !present || raw == nil {
		return SsrfConfig{}, nil
	}
	ssrf, ok := asObject(raw)
	if !ok {
		return SsrfConfig{}, fmt.Errorf("ssrf in %s must be an object", path)
	}
	ranges, hasRanges := ssrf["allowRanges"]
	var list []any
	if hasRanges && ranges != nil {
		var isList bool
		list, isList = ranges.([]any)
		if !isList {
			return SsrfConfig{}, fmt.Errorf("ssrf.allowRanges in %s must be an array of CIDR strings", path)
		}
	}
	trust, hasTrust := ssrf["trustEnvProxy"]
	if b, isBool := trust.(bool); hasTrust && !isBool {
		_ = b
		return SsrfConfig{}, fmt.Errorf("ssrf.trustEnvProxy in %s must be a boolean", path)
	}
	out := SsrfConfig{TrustEnvProxy: trust == true}
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return SsrfConfig{}, fmt.Errorf("ssrf.allowRanges in %s must contain only CIDR strings; entry %d is %s", path, i+1, jsType(e))
		}
		if s = strings.TrimSpace(s); s != "" {
			out.AllowRanges = append(out.AllowRanges, s)
		}
	}
	if _, err := ParseAllowRanges(out.AllowRanges); err != nil {
		return SsrfConfig{}, err
	}
	return out, nil
}

var hostnamePattern = regexp.MustCompile(`(?i)^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
var domainForbidden = regexp.MustCompile(`\s|[\\/?:#@]`)

func normalizeDomainEntry(entry string) (string, bool) {
	hostname := normalizeHostname(strings.TrimSpace(entry))
	if hostname == "" || domainForbidden.MatchString(hostname) {
		return "", false
	}
	if ipVersion(hostname) != 0 {
		return hostname, true
	}
	if len(hostname) > 253 || !hostnamePattern.MatchString(hostname) {
		return "", false
	}
	return hostname, true
}

func parseDomainEntries(value any, field string) ([]string, error) {
	path := ConfigPath()
	if value == nil {
		return nil, nil
	}
	list, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("fetchContent.domainPolicy.%s in %s must be an array of hostnames", field, path)
	}
	out := make([]string, 0, len(list))
	for i, e := range list {
		s, ok := e.(string)
		if !ok {
			return nil, fmt.Errorf("fetchContent.domainPolicy.%s in %s must contain only hostnames; entry %d is %s", field, path, i+1, jsType(e))
		}
		h, ok := normalizeDomainEntry(s)
		if !ok {
			return nil, fmt.Errorf("fetchContent.domainPolicy.%s in %s contains an invalid hostname: %s", field, path, jsonString(s))
		}
		out = append(out, h)
	}
	return out, nil
}

// LoadFetchContentDomainPolicy reads fetchContent.domainPolicy from web-search.json.
func LoadFetchContentDomainPolicy() (DomainPolicy, error) {
	root, err := ReadConfigRoot()
	if err != nil || root == nil {
		return DomainPolicy{}, err
	}
	path := ConfigPath()
	fc, present := root["fetchContent"]
	if !present || fc == nil {
		return DomainPolicy{}, nil
	}
	fcObj, ok := asObject(fc)
	if !ok {
		return DomainPolicy{}, fmt.Errorf("fetchContent in %s must be an object", path)
	}
	policy, present := fcObj["domainPolicy"]
	if !present || policy == nil {
		return DomainPolicy{}, nil
	}
	pObj, ok := asObject(policy)
	if !ok {
		return DomainPolicy{}, fmt.Errorf("fetchContent.domainPolicy in %s must be an object", path)
	}
	allow, err := parseDomainEntries(pObj["allow"], "allow")
	if err != nil {
		return DomainPolicy{}, err
	}
	deny, err := parseDomainEntries(pObj["deny"], "deny")
	if err != nil {
		return DomainPolicy{}, err
	}
	return DomainPolicy{Allow: allow, Deny: deny}, nil
}
