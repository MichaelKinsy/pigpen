package websearch

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

type proxyKey struct{}

type proxyDecision struct {
	proxy string // "" means: forced direct access
}

// WithProxy scopes a proxy decision to ctx, like `runWithProxy(proxy, fn)` with a value:
// a non-empty proxy routes every request of the call through it, "" forces direct access.
func WithProxy(ctx context.Context, proxy string) context.Context {
	return context.WithValue(ctx, proxyKey{}, proxyDecision{proxy: proxy})
}

// ScopeProxy is `runWithProxy(proxy, fn)`: a call parameter (nil when omitted) wins; else
// `proxy` in web-search.json applies; else there is no scoped decision. Invalid values fail
// closed with the original's messages instead of falling back to direct access.
func ScopeProxy(ctx context.Context, param *string) (context.Context, error) {
	if param == nil {
		configured, err := loadConfiguredProxy()
		if err != nil {
			return ctx, err
		}
		if configured == "" {
			return ctx, nil
		}
		return WithProxy(ctx, configured), nil
	}
	normalized, _, err := NormalizeProxyURL(*param, "proxy")
	if err != nil {
		return ctx, err
	}
	return WithProxy(ctx, normalized), nil
}

func activeProxy(ctx context.Context) string {
	if d, ok := ctx.Value(proxyKey{}).(proxyDecision); ok {
		return d.proxy
	}
	return ""
}

func hasScopedProxyDecision(ctx context.Context) bool {
	_, ok := ctx.Value(proxyKey{}).(proxyDecision)
	return ok
}

func loadConfiguredProxy() (string, error) {
	root, err := ReadConfigRoot()
	if err != nil {
		path := ConfigPath()
		return "", fmt.Errorf("Failed to load proxy config from %s: %s", path, strings.TrimPrefix(err.Error(), "Failed to parse "+path+": "))
	}
	value, present := root["proxy"]
	if !present {
		return "", nil
	}
	normalized, _, err := NormalizeProxyURL(value, "proxy in "+ConfigPath())
	return normalized, err
}

// NormalizeProxyURL validates a proxy URL: http(s) or socks, with a host. It returns the
// normalized value without query and fragment; empty input means "no proxy".
func NormalizeProxyURL(value any, source string) (string, bool, error) {
	if value == nil {
		return "", false, nil
	}
	s, ok := value.(string)
	if !ok {
		return "", false, fmt.Errorf("%s must be an http(s) or socks proxy URL string", source)
	}
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return "", false, nil
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Scheme == "" || (parsed.Host == "" && parsed.Opaque == "") {
		return "", false, fmt.Errorf("%s must be a valid proxy URL: %s", source, jsonString(trimmed))
	}
	switch parsed.Scheme {
	case "http", "https", "socks4", "socks4a", "socks5", "socks5h":
	default:
		return "", false, fmt.Errorf("%s must use the http://, https://, or socks scheme: %s", source, trimmed)
	}
	if parsed.Hostname() == "" {
		return "", false, fmt.Errorf("%s must include a proxy host: %s", source, trimmed)
	}
	parsed.Fragment, parsed.RawQuery, parsed.ForceQuery = "", "", false
	out := parsed.String()
	return out, true, nil
}

// RedactProxyURL hides proxy credentials in messages.
func RedactProxyURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.User == nil {
		return value
	}
	name := parsed.User.Username()
	if name != "" {
		name = "redacted"
	}
	if _, hasPassword := parsed.User.Password(); hasPassword {
		parsed.User = url.UserPassword(name, "redacted")
	} else {
		parsed.User = url.User(name)
	}
	return parsed.String()
}
