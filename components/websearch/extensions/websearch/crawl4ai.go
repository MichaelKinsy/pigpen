package websearch

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// Port of crawl4ai.ts: a self-hosted Crawl4AI instance (base URL required), POST /md with the
// "fit" markdown filter. The configured origin may be loopback; a redirect to another origin is
// refused before it is fetched.

const (
	crawl4aiTimeout        = 60 * time.Second
	crawl4aiMarkdownFilter = "fit"
)

func crawl4aiBase() (string, bool, error) {
	if base, ok, err := normalizeConfiguredBase(envOrNil("CRAWL4AI_BASE_URL"), "Crawl4AI"); err != nil || ok {
		return base, ok, err
	}
	return normalizeConfiguredBase(configAny("crawl4aiBaseUrl"), "Crawl4AI")
}

func crawl4aiRequireBase() (string, error) {
	base, ok, err := crawl4aiBase()
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("Crawl4AI base URL not configured. Either:\n  1. Set crawl4aiBaseUrl in %s\n  2. Set CRAWL4AI_BASE_URL environment variable", ConfigPath())
	}
	return base, nil
}

// IsCrawl4aiAvailable reports whether a base URL is configured.
func IsCrawl4aiAvailable() bool {
	_, ok, err := crawl4aiBase()
	return err == nil && ok
}

// crawl4aiConfigured is the routing availability: a malformed base URL is the provider's error.
func crawl4aiConfigured() (bool, error) {
	_, ok, err := crawl4aiBase()
	return ok, err
}

var crawl4aiHeadingRE = regexp.MustCompile(`(?m)^[ \t]*#[ \t]+(\S.*?)[ \t\r]*$`)

// ExtractWithCrawl4ai fetches one URL through the configured Crawl4AI instance.
func ExtractWithCrawl4ai(ctx context.Context, rawURL string, o ExtractOptions, ssrfOpt *SsrfConfig) (*ExtractedContent, error) {
	base, err := crawl4aiRequireBase()
	if err != nil {
		return nil, err
	}
	ssrf, err := firecrawlSSRF(ssrfOpt)
	if err != nil {
		return nil, err
	}
	if err := validateTarget(ctx, rawURL, ssrf, o.Lookup, false); err != nil {
		return nil, err
	}
	token, err := resolveProviderCredential(ctx, "Crawl4AI", "crawl4aiApiToken", "CRAWL4AI_API_TOKEN")
	if err != nil {
		return nil, err
	}
	h := jsonHeader()
	if token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	redact := func(s string) string {
		if token == "" {
			return s
		}
		return RedactCredential(s, token)
	}
	requestURL := base + "/md"
	init := RequestInit{Method: "POST", Header: h, Body: jsonBytes(map[string]any{"url": rawURL, "f": crawl4aiMarkdownFilter})}
	seeOther := false
	origin := ""
	if u, err := ParseURL(requestURL, nil); err == nil {
		origin = Origin(u)
	}
	act := logFetchStart(requestURL)
	resp, tctx, cancel, err := guardedAPI(ctx, requestURL, init, timeoutMsOr(o.TimeoutMs, crawl4aiTimeout), FetchRemoteOptions{
		ValidationOptions: ssrfValidation(ssrf, o.Lookup, isLoopbackHost(requestURL)),
		OnRedirectErr: func(a RedirectArgs) (RequestInit, error) {
			if Origin(a.To) != origin {
				return a.Init, fmt.Errorf("Crawl4AI refused cross-origin redirect to %s", Origin(a.To))
			}
			// 303 means "GET the other resource" and is final: once a hop turned the chain into a
			// GET, no later hop resurrects the extraction body. 301/302 are replayed as the POST.
			if a.Response.StatusCode == 303 {
				seeOther = true
			}
			if !seeOther && (a.Response.StatusCode == 301 || a.Response.StatusCode == 302) {
				return init, nil
			}
			return a.Init, nil
		},
	})
	defer cancel()
	if err != nil {
		return nil, act.failedBy(ctx, timeoutFix(ctx, tctx, err), "")
	}
	text, rerr := readBody(resp)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		if rerr != nil {
			text = ""
		}
		return nil, act.failedBy(ctx, fmt.Errorf("Crawl4AI md error %d: %s", resp.StatusCode, jsSlice(redact(text), 0, 300)), "")
	}
	if rerr != nil {
		return nil, act.failedBy(ctx, timeoutFix(ctx, tctx, rerr), "")
	}
	data, err := decodeAny(text)
	if err != nil {
		// The parser message quotes the body, which can echo the token.
		return nil, act.failedBy(ctx, errors.New("Crawl4AI md returned invalid JSON"), "")
	}
	env := asMap(data)
	if env == nil {
		return nil, act.failedBy(ctx, errors.New("Crawl4AI md returned an unexpected response shape"), "")
	}
	switch env["success"] {
	case false:
		detail := ""
		if s, ok := strField(env, "error"); ok {
			detail = s
		} else if s, ok := strField(env, "detail"); ok {
			detail = s
		}
		if jsTrim(detail) == "" {
			detail = "unknown error"
		} else {
			detail = jsTrim(detail)
		}
		return nil, act.failedBy(ctx, fmt.Errorf("Crawl4AI md unsuccessful: %s", redact(detail)), "")
	case true:
	default:
		return nil, act.failedBy(ctx, errors.New("Crawl4AI md returned an unexpected response shape"), "")
	}
	md, ok := strField(env, "markdown")
	if !ok {
		return nil, act.failedBy(ctx, errors.New("Crawl4AI md returned markdown in an unexpected shape"), "")
	}
	act.complete(resp.StatusCode)
	content := jsTrim(md)
	if content == "" {
		return nil, nil
	}
	title := ""
	if m := crawl4aiHeadingRE.FindStringSubmatch(content); m != nil {
		title = m[1]
	}
	return &ExtractedContent{URL: rawURL, Title: title, Content: content}, nil
}
