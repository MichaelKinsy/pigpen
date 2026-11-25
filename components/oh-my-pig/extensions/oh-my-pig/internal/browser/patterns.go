package browser

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// URLMatcher tests whether a URL matches a pattern (substring, glob with `*`/`**`,
// or serialized RegExp `{__omp_re: {source, flags}}`).
// upstream: urlPattern + urlMatcher in network.ts, parseUrlPattern in navigation.ts:21
type URLMatcher func(rawURL string) bool

// GlobToRegex converts an omp URL glob (`*` within path segment except at
// boundaries, `**` across segments) to a Go regexp.
// upstream: urlPattern in network.ts
func GlobToRegex(glob string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(glob)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '*' {
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
				continue
			}
			// Leading or trailing * spans path segments (matches omp's browser-url-patterns.test.ts).
			if i == 0 || i == len(runes)-1 {
				b.WriteString(".*")
			} else {
				b.WriteString("[^/]*")
			}
			continue
		}
		b.WriteString(regexp.QuoteMeta(string(runes[i])))
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// ParseURLPattern compiles a string (substring or `*` glob) or `{__omp_re: {source, flags}}`
// object into a URLMatcher.
// upstream: parseUrlPattern in navigation.ts:21
func ParseURLPattern(raw any) (URLMatcher, error) {
	switch v := raw.(type) {
	case string:
		if strings.Contains(v, "*") {
			re, err := GlobToRegex(v)
			if err != nil {
				return nil, fmt.Errorf("invalid URL glob %q: %w", v, err)
			}
			return re.MatchString, nil
		}
		return func(u string) bool { return strings.Contains(u, v) }, nil
	case map[string]any:
		if reObj, ok := v["__omp_re"].(map[string]any); ok {
			src, _ := reObj["source"].(string)
			flags, _ := reObj["flags"].(string)
			prefix := ""
			if strings.Contains(flags, "i") {
				prefix += "i"
			}
			if strings.Contains(flags, "m") {
				prefix += "m"
			}
			if strings.Contains(flags, "s") {
				prefix += "s"
			}
			if prefix != "" {
				src = "(?" + prefix + ")" + src
			}
			re, err := regexp.Compile(src)
			if err != nil {
				return nil, fmt.Errorf("invalid URL regexp %q: %w", src, err)
			}
			return re.MatchString, nil
		}
		if _, isFn := v["__omp_fn"]; isFn {
			return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not a function")
		}
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not object")
	case nil:
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not null")
	default:
		return nil, fmt.Errorf("tab.waitForUrl() takes a string the URL contains, a glob with * (**/path), or a RegExp it matches, not %T", raw)
	}
}

// NormalizeAllowedDomains validates and normalizes an allowed_domains list
// (lowercase, no trailing dot, `*.suffix` wildcards, deduped), rejecting
// malformed patterns with omp's exact error message.
// upstream: normalizeAllowedDomains in network.ts:556-576
func NormalizeAllowedDomains(domains []string) ([]string, error) {
	if len(domains) == 0 {
		return nil, nil
	}
	normalized := make([]string, 0, len(domains))
	seen := make(map[string]bool, len(domains))
	for _, value := range domains {
		domain := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(value)), ".")
		if domain == "" ||
			strings.Contains(domain, "://") ||
			strings.Contains(domain, "/") ||
			(strings.Contains(domain, "*") && !strings.HasPrefix(domain, "*.")) {
			return nil, fmt.Errorf("Invalid allowed_domains pattern %q", value)
		}
		suffix := strings.TrimPrefix(domain, "*.")
		if suffix == "" || strings.Contains(suffix, "*") {
			return nil, fmt.Errorf("Invalid allowed_domains pattern %q", value)
		}
		if !seen[domain] {
			seen[domain] = true
			normalized = append(normalized, domain)
		}
	}
	return normalized, nil
}

// sameAllowedDomains reports whether two normalized allowed-domain lists match.
// upstream: sameAllowedDomains in tab-supervisor.ts
func sameAllowedDomains(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// MatchAllowedDomain checks whether rawURL is allowed by patterns
// (exact hostnames or `*.example.com`, where bare `example.com` also matches).
// Non-HTTP(S)/WS(S) schemes (about:, data:, blob:) are always permitted.
// upstream: domainMatches in network.ts:578-582
func MatchAllowedDomain(rawURL string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" && scheme != "ws" && scheme != "wss" {
		return true
	}
	host := strings.ToLower(u.Hostname())
	for _, pat := range patterns {
		p := strings.ToLower(strings.TrimSpace(pat))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "*.") {
			suffix := p[2:]
			if host == suffix || strings.HasSuffix(host, "."+suffix) {
				return true
			}
		} else if host == p {
			return true
		}
	}
	return false
}

// RedactSensitiveHeaders returns a copy of headers with credential headers masked.
// upstream: toPublicRecord in network.ts
func RedactSensitiveHeaders(headers map[string]string) map[string]string {
	if headers == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		lk := strings.ToLower(k)
		if lk == "cookie" || lk == "set-cookie" || lk == "authorization" || lk == "proxy-authorization" ||
			strings.Contains(lk, "token") || strings.Contains(lk, "api-key") {
			out[k] = "[redacted]"
		} else {
			out[k] = v
		}
	}
	return out
}

// RoleQuery is a parsed `role/<role>[name="…" exact]` selector.
// upstream: parseRoleSelector in query-handlers.ts:558
type RoleQuery struct {
	Role    string
	Name    string
	HasName bool
	Exact   bool
}

var (
	roleSelectorRE  = regexp.MustCompile(`(?i)^\s*([a-z][a-z0-9-]*)\s*(?:\[\s*name\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\]\s]+))(?:\s+(exact))?\s*\])?\s*$`)
	trailingExactRE = regexp.MustCompile(`(?i)\s+exact$`)
	collapseSpaceRE = regexp.MustCompile(`\s+`)
)

// ParseRoleSelector parses the body of a `role/` selector.
// upstream: parseRoleSelector in query-handlers.ts:558
func ParseRoleSelector(body string) (RoleQuery, error) {
	m := roleSelectorRE.FindStringSubmatch(body)
	if m == nil {
		return RoleQuery{}, fmt.Errorf(
			`role/ selector %q is not one role with an optional [name="…"] or [name="…" exact] filter. `+
				"role/ selectors do not chain; to act inside a section, pass the section's selector to observe({ selector }) or ariaSnapshot(selector) and use the id or ref it returns.",
			body,
		)
	}
	role := strings.ToLower(m[1])
	var rawName string
	hasName := false
	switch {
	case m[2] != "":
		rawName, hasName = m[2], true
	case m[3] != "":
		rawName, hasName = m[3], true
	case m[4] != "":
		rawName, hasName = m[4], true
	default:
		if strings.Contains(body, `name=""`) || strings.Contains(body, `name=''`) {
			hasName = true
		}
	}
	exact := m[5] != ""
	if hasName && trailingExactRE.MatchString(rawName) {
		rawName = trailingExactRE.ReplaceAllString(rawName, "")
		exact = true
	}
	if hasName {
		rawName = strings.ToLower(strings.TrimSpace(collapseSpaceRE.ReplaceAllString(rawName, " ")))
	}
	return RoleQuery{
		Role:    role,
		Name:    rawName,
		HasName: hasName,
		Exact:   exact,
	}, nil
}

// ParseAriaRefSelector extracts `eN` from `aria-ref=e5`, `aria-ref/e5`,
// `ariaref/e5`, `e5`, or `@e5`.
// upstream: parseAriaRefSelector in aria/aria-snapshot.ts:146
var (
	ariaRefIDRE   = regexp.MustCompile(`^e\d+$`)
	ariaRefBareRE = regexp.MustCompile(`^@?(e\d+)$`)
)

func ParseAriaRefSelector(selector string) string {
	trimmed := strings.TrimSpace(selector)
	for _, prefix := range []string{"aria-ref=", "aria-ref/", "ariaref/"} {
		if strings.HasPrefix(trimmed, prefix) {
			id := strings.TrimSpace(trimmed[len(prefix):])
			if ariaRefIDRE.MatchString(id) {
				return id
			}
			return ""
		}
	}
	if m := ariaRefBareRE.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	return ""
}

// playwrightOnlySelectorRE guards against unsupported Playwright-only pseudos.
// upstream: PLAYWRIGHT_ONLY_SELECTOR_RE in aria/aria-snapshot.ts:101
var playwrightOnlySelectorRE = regexp.MustCompile(`:has-text\(|:text\(|:text-is\(|:text-matches\(|:visible\b|:hidden\b|:nth-match\(|:near\(|:above\(|:below\(|:right-of\(|:left-of\(`)

// ValidateSelector rejects Playwright-only pseudo-selectors on bare CSS selectors.
func ValidateSelector(sel string) error {
	trimmed := strings.TrimSpace(sel)
	if trimmed == "" {
		return fmt.Errorf("selector must be a non-empty string")
	}
	for _, prefix := range []string{"text/", "aria/", "xpath/", "pierce/", "label/", "placeholder/", "testid/", "alt/", "title/", "role/", "css/", "aria-ref=", "aria-ref/", "ariaref/"} {
		if strings.HasPrefix(trimmed, prefix) {
			return nil
		}
	}
	if playwrightOnlySelectorRE.MatchString(trimmed) {
		return fmt.Errorf("Playwright-only selector %q is not supported; use CSS or aria/, text/, xpath/, pierce/, label/, placeholder/, testid/, alt/, title/, role/ selectors.", sel)
	}
	return nil
}

// DecodePreludeFn extracts the JavaScript source from a prelude-encoded function
// marker `{"__omp_fn": "..."}`.
func DecodePreludeFn(raw any) (string, bool) {
	m, ok := raw.(map[string]any)
	if !ok || len(m) != 1 {
		return "", false
	}
	src, ok := m["__omp_fn"].(string)
	return src, ok
}

// DecodePreludeRegExp extracts (source, flags) from a prelude-encoded RegExp
// marker `{"__omp_re": {"source": "...", "flags": "..."}}`.
func DecodePreludeRegExp(raw any) (source, flags string, ok bool) {
	m, isMap := raw.(map[string]any)
	if !isMap || len(m) != 1 {
		return "", "", false
	}
	reObj, isMap := m["__omp_re"].(map[string]any)
	if !isMap {
		return "", "", false
	}
	src, hasSrc := reObj["source"].(string)
	if !hasSrc {
		return "", "", false
	}
	fl, _ := reObj["flags"].(string)
	return src, fl, true
}
