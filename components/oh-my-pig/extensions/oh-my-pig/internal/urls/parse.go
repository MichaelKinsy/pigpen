package urls

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// upstream: packages/coding-agent/src/internal-urls/parse.ts

var (
	schemeHostRE = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*):\/\/([^/?#]*)`)
	pathnameRE   = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*:\/\/[^/?#]*(\/[^?#]*)?`)
	// Opaque URI form (urn:example:document, custom:item) — an RFC 3986 scheme followed by ':' without '//'.
	opaqueURIRE = regexp.MustCompile(`(?is)^([a-z][a-z0-9+.-]*):(.+)$`)
	// A read-tool selector chain (12, 1-20,30+5, raw, raw:2-4, conflicts)
	// so Makefile:12 or notes:raw is not mistaken for an opaque URI.
	selectorChunkSrc = `(?:raw|conflicts|-?\d+(?:[-+]\d+)?(?:,\d+(?:[-+]\d+)?)*)`
	selectorChainRE  = regexp.MustCompile(`(?i)^` + selectorChunkSrc + `(?::` + selectorChunkSrc + `)*(?:;|$)`)
	urlSchemeRE      = regexp.MustCompile(`(?i)^([a-z][a-z0-9+.-]*):\/\/`)
)

// SplitScheme splits a hierarchical `scheme://rest` URL into its lowercased scheme and the text after `://`.
// upstream: packages/tui/src/tools/url-scheme-host.ts splitUrlScheme.
func SplitScheme(input string) (scheme, rest string, ok bool) {
	m := urlSchemeRE.FindStringSubmatchIndex(input)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(input[m[2]:m[3]]), input[m[1]:], true
}

// ExtractScheme extracts the lowercased scheme from a URI-shaped input, or ""
// when the input does not look like a URI.
// Accepts both hierarchical (`scheme://...`) and opaque (`scheme:rest`) forms.
// upstream: parse.ts extractUriScheme.
func ExtractScheme(input string) string {
	if m := schemeHostRE.FindStringSubmatch(input); m != nil {
		return strings.ToLower(m[1])
	}
	m := opaqueURIRE.FindStringSubmatch(input)
	if m == nil {
		return ""
	}
	scheme, rest := m[1], m[2]
	if len(scheme) == 1 {
		return ""
	}
	if strings.ContainsRune(scheme, '.') {
		return ""
	}
	if selectorChainRE.MatchString(rest) {
		return ""
	}
	return strings.ToLower(scheme)
}

// parseInternal parses an internal URL into URL, preserving raw host (with colons/case),
// raw pathname (with traversal markers), raw query, and raw href.
// upstream: parse.ts parseInternalUrl.
func parseInternal(input string) (URL, error) {
	hostMatch := schemeHostRE.FindStringSubmatch(input)
	pathMatch := pathnameRE.FindStringSubmatch(input)

	// Extract query and fragment from raw input directly so malformed ports or colons in host
	// (e.g. skill://plugin:name) never fail.
	hashIdx := strings.IndexByte(input, '#')
	withoutHash := input
	if hashIdx != -1 {
		withoutHash = input[:hashIdx]
	}
	queryIdx := strings.IndexByte(withoutHash, '?')
	rawQuery := ""
	if queryIdx != -1 {
		rawQuery = withoutHash[queryIdx+1:]
	}

	var scheme, rawHost, rawPath string
	if hostMatch != nil {
		scheme = strings.ToLower(hostMatch[1])
		rawHost = hostMatch[2]
		if pathMatch != nil {
			rawPath = pathMatch[1]
		}
		if queryIdx != -1 {
			if qi := strings.IndexByte(rawPath, '?'); qi != -1 {
				rawPath = rawPath[:qi]
			}
		}
	} else {
		// Try standard net/url for opaque URIs (e.g. urn:example:document).
		parsed, err := url.Parse(input)
		if err != nil || parsed.Scheme == "" {
			return URL{}, fmt.Errorf("Invalid URL: %s", input)
		}
		scheme = strings.ToLower(parsed.Scheme)
		rawHost = parsed.Host
		if parsed.Opaque != "" {
			rawPath = parsed.Opaque
		} else {
			rawPath = parsed.Path
		}
		if rawQuery == "" {
			rawQuery = parsed.RawQuery
		}
	}

	decodedHost := rawHost
	if u, err := url.PathUnescape(rawHost); err == nil {
		decodedHost = u
	}

	parsedQuery := parseQueryLenient(rawQuery)
	return URL{
		Href:     input,
		RawHref:  input,
		Scheme:   scheme,
		Host:     decodedHost,
		Path:     rawPath,
		Query:    parsedQuery,
		RawQuery: rawQuery,
	}, nil
}

// parseQueryLenient mirrors WHATWG URLSearchParams: splits only on `&` (never
// rejecting `;` or malformed percent-escapes like net/url.ParseQuery does).
func parseQueryLenient(raw string) url.Values {
	v := url.Values{}
	if raw == "" {
		return v
	}
	for _, pair := range strings.Split(raw, "&") {
		if pair == "" {
			continue
		}
		k, val, _ := strings.Cut(pair, "=")
		if uk, err := url.QueryUnescape(k); err == nil {
			k = uk
		}
		if uv, err := url.QueryUnescape(val); err == nil {
			val = uv
		}
		v.Add(k, val)
	}
	return v
}

// extractRawAuthority returns the undecoded authority segment from rawHref (after `scheme://`,
// before `/`, `?`, or `#`). Useful for schemes like local:// where authority is the first path
// segment and should be decoded once with the rest of the path.
func extractRawAuthority(u URL) string {
	if m := schemeHostRE.FindStringSubmatch(u.RawHref); m != nil {
		return m[2]
	}
	return u.Host
}

// extractHash returns the `#...` fragment from rawHref, or "" if none.
func extractHash(u URL) string {
	if idx := strings.IndexByte(u.RawHref, '#'); idx != -1 {
		return u.RawHref[idx:]
	}
	return ""
}
