package websearch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

// SearchOptions are the per-search options every provider receives.
type SearchOptions struct {
	// NumResults is nil when unset; providers normalise NaN, negative and fractional values.
	NumResults     *float64
	RecencyFilter  string
	DomainFilter   []string
	IncludeContent bool
}

// SearchResponse is what a provider returns.
type SearchResponse struct {
	Answer        string             `json:"answer"`
	Results       []SearchResult     `json:"results"`
	InlineContent []ExtractedContent `json:"inlineContent,omitempty"`
}

// NormalizeSearchResultCount clamps to 1..20, default 5 (search-result-count-normalization.ts).
func NormalizeSearchResultCount(v *float64) int {
	if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
		return 5
	}
	n := math.Floor(*v)
	if n > 20 {
		n = 20
	}
	if n < 1 {
		n = 1
	}
	return int(n)
}

var domainShape = regexp.MustCompile(`(?i)^[a-z0-9][a-z0-9.-]*\.[a-z]{2,}$`)

// NormalizeDomain reduces a filter entry ("-https://Docs.Example.com/x") to its hostname, or "" when
// it is not a domain (domain-filter-normalization.ts).
func NormalizeDomain(value string) string {
	input := strings.ToLower(jsTrim(value))
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "-") {
		input = jsTrim(input[1:])
	}
	if input == "" {
		return ""
	}
	candidate := input
	if !strings.Contains(candidate, "://") {
		candidate = "https://" + candidate
	}
	if u, err := ParseURL(candidate, nil); err == nil {
		input = JSHostname(u)
	} else {
		input, _, _ = strings.Cut(input, "/")
		input, _, _ = strings.Cut(input, ":")
	}
	input = strings.Trim(input, ".")
	if domainShape.MatchString(input) {
		return input
	}
	return ""
}

// domainFilters is the normalised, deduplicated include/exclude split of a domainFilter.
type domainFilters struct{ allowed, blocked []string }

func normalizeDomainFilters(filter []string) domainFilters {
	var out domainFilters
	for _, raw := range filter {
		d := NormalizeDomain(raw)
		if d == "" {
			continue
		}
		target := &out.allowed
		if strings.HasPrefix(jsTrim(raw), "-") {
			target = &out.blocked
		}
		if !sliceHas(*target, d) {
			*target = append(*target, d)
		}
	}
	return out
}

func sliceHas(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hostMatchesDomain(hostname, domain string) bool {
	return hostname == domain || strings.HasSuffix(hostname, "."+domain)
}

// matches reports whether a result URL passes the filters (unparseable URLs do not).
func (f domainFilters) matches(rawURL string) bool {
	if len(f.allowed) == 0 && len(f.blocked) == 0 {
		return true
	}
	u, err := ParseURL(rawURL, nil)
	if err != nil {
		return false
	}
	host := strings.ToLower(JSHostname(u))
	if len(f.allowed) > 0 {
		ok := false
		for _, d := range f.allowed {
			ok = ok || hostMatchesDomain(host, d)
		}
		if !ok {
			return false
		}
	}
	for _, d := range f.blocked {
		if hostMatchesDomain(host, d) {
			return false
		}
	}
	return true
}

// formEscape is application/x-www-form-urlencoded percent-encoding (URLSearchParams).
func formEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// configString reads a web-search.json value; a config that cannot be read counts as absent here
// (the parse error surfaces from Search, which loads the config first).
func configAny(key string) any { return configValue(key) }

func resolveProviderCredential(ctx context.Context, provider, configKey, envName string) (string, error) {
	return ResolveCredential(ctx, CredentialOptions{Provider: provider, ConfiguredValue: configAny(configKey), EnvironmentValue: envOrNil(envName)})
}

func hasProviderCredential(provider, configKey, envName string) bool {
	return HasCredentialSource(CredentialOptions{Provider: provider, ConfiguredValue: configAny(configKey), EnvironmentValue: envOrNil(envName)})
}

func apiBase(configKey, envKey, def string) (string, error) {
	return ResolveAPIBaseURL(APIBaseURLOptions{ConfigKey: configKey, ConfiguredValue: configAny(configKey), DefaultValue: def, EnvironmentKey: envKey, EnvironmentValue: envPtr(envKey)})
}

func envPtr(name string) *string {
	if v, ok := os.LookupEnv(name); ok {
		return &v
	}
	return nil
}

// redactedError carries the redacted message and keeps the original error for errors.Is.
type redactedError struct {
	msg   string
	cause error
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.cause }

// redactErr removes the credential from an error's message.
func redactErr(err error, secret string) error {
	if err == nil || secret == "" {
		return err
	}
	msg := err.Error()
	if r := RedactCredential(msg, secret); r != msg {
		return &redactedError{r, err}
	}
	return err
}

// withTimeout bounds one provider request. A request that hit its own deadline (as opposed to
// the caller's cancellation) becomes a "timed out" error so routing can treat it as a network
// failure and fall through; the original treats every AbortSignal.timeout as an abort.
func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, d)
}

func timeoutFix(parent, child context.Context, err error) error {
	if err != nil && parent.Err() == nil && child.Err() == context.DeadlineExceeded {
		return &redactedError{"request timed out (" + err.Error() + ")", err}
	}
	return err
}

func errorMessage(err error) string { return err.Error() }

// isAbortError is true for the caller's cancellation (or any error that says it aborted).
func isAbortError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "abort")
}

const maxProviderBody = 16 << 20

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxProviderBody))
	return string(b), err
}

func slice300(s string) string { return jsSlice(s, 0, 300) }

var whitespaceRun = regexp.MustCompile(`[\s\x{FEFF}]+`)

func collapseWS(s string) string { return jsTrim(whitespaceRun.ReplaceAllString(s, " ")) }

// sourceLines is the answer text of providers that do not synthesize one.
func sourceLines(results []SearchResult) string {
	parts := make([]string, len(results))
	for i, r := range results {
		if r.Snippet != "" {
			parts[i] = r.Snippet + "\nSource: " + r.Title + " (" + r.URL + ")"
		} else {
			parts[i] = "Source: " + r.Title + " (" + r.URL + ")"
		}
	}
	return strings.Join(parts, "\n\n")
}
