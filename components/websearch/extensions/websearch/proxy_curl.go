package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Proxied requests run `curl` through the host, exactly as utils.ts fetchViaCurl does (the
// arguments, the header dump, the write-out JSON, the temporary directory), so every proxy
// scheme curl speaks works, SOCKS4 and SOCKS4a included. The original spawns curl with Node's
// spawn and the process environment; here the host's command runner (pi.exec in the original's
// own terms) starts it with the host's environment. Only a single hop is made: every caller
// follows redirects by hand (utils.ts answers a manual redirect with the redirect response).

// curlError is utils.ts CurlTransportError.
type curlError struct{ msg string }

func (e *curlError) Error() string { return e.msg }

// maxCurlBody bounds the body read back from curl's output file. The original reads the whole
// file; this port refuses what could not be an answer to a search or a page request before it
// is held in memory (the page limit of 5 MB and the API limit of 16 MB sit far below).
const maxCurlBody = 64 << 20

// isProxyBypassedURL is utils.ts isProxyBypassedUrl: loopback names and NO_PROXY entries are not
// sent through the active proxy.
func isProxyBypassedURL(u *url.URL) bool {
	host := strings.ToLower(strings.Trim(u.Hostname(), "[]"))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "127.0.0.1" || host == "::1" {
		return true
	}
	for _, entry := range strings.Split(envNoProxy(), ",") {
		if noProxyEntryMatches(host, strings.TrimSpace(entry)) {
			return true
		}
	}
	return false
}

// curlHeaderArgs are the `-H` arguments of new Headers(init.headers).entries(): names
// lowercased, sorted, equal names joined with ", ", empty values dropped.
func curlHeaderArgs(h http.Header) []string {
	combined := map[string][]string{}
	// A Go map has no insertion order: names that differ only in case merge in their sorted order.
	raw := make([]string, 0, len(h))
	for name := range h {
		raw = append(raw, name)
	}
	sort.Strings(raw)
	for _, name := range raw {
		lower := strings.ToLower(name)
		combined[lower] = append(combined[lower], h[name]...)
	}
	names := make([]string, 0, len(combined))
	for name := range combined {
		names = append(names, name)
	}
	sort.Strings(names)
	var args []string
	for _, name := range names {
		value := strings.Join(combined[name], ", ")
		if value == "" {
			continue
		}
		args = append(args, "-H", name+": "+value)
	}
	return args
}

var (
	dumpBlockSplit = regexp.MustCompile(`\r?\n\r?\n`)
	dumpStatusLine = regexp.MustCompile(`^HTTP/[\d.]+\s+(\d{3})(?:\s+(.*))?$`)
	dumpStatusHead = regexp.MustCompile(`^HTTP/[\d.]+\s+\d{3}`)
)

// parseHeaderDump is utils.ts parseHeaderDump: the last block that starts with a status line.
func parseHeaderDump(dump string) (status int, statusText string, headers http.Header) {
	headers = http.Header{}
	var block string
	for _, b := range dumpBlockSplit.Split(dump, -1) {
		if dumpStatusHead.MatchString(jsTrim(b)) {
			block = b
		}
	}
	for _, line := range regexp.MustCompile(`\r?\n`).Split(jsTrim(block), -1) {
		if m := dumpStatusLine.FindStringSubmatch(jsTrim(line)); m != nil {
			status, _ = strconv.Atoi(m[1])
			statusText = m[2]
			headers = http.Header{}
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			name := jsTrim(line[:i])
			if lower := strings.ToLower(name); lower == "content-encoding" || lower == "content-length" {
				continue
			}
			// new Headers(pairs) joins equal names with ", "; Set-Cookie stays separate.
			value := jsTrim(line[i+1:])
			if prev := headers.Get(name); prev != "" && !strings.EqualFold(name, "Set-Cookie") {
				headers.Set(name, prev+", "+value)
			} else {
				headers.Add(name, value)
			}
		}
	}
	return status, statusText, headers
}

// abortedRequest is the AbortError of a request cancelled before or while curl ran.
type abortedRequest struct{ cause error }

func (e *abortedRequest) Error() string { return "The operation was aborted." }
func (e *abortedRequest) Unwrap() error { return e.cause }

func runCurl(ctx context.Context, args []string) (ExecResult, error) {
	type outcome struct {
		res ExecResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := runHostCommand(ctx, "curl", args, 0)
		done <- outcome{res, err}
	}()
	select {
	case o := <-done:
		return o.res, o.err
	case <-ctx.Done():
		return ExecResult{}, &abortedRequest{ctx.Err()}
	}
}

// curlFetchOnce is utils.ts fetchViaCurlOnce: one request through curl, answered with the
// response curl dumped.
func curlFetchOnce(ctx context.Context, u *url.URL, init RequestInit, proxyURL string) (*http.Response, error) {
	method := strings.ToUpper(init.Method)
	if method == "" {
		method = "GET"
	}
	if ctx.Err() != nil {
		return nil, &abortedRequest{ctx.Err()}
	}
	dir, err := os.MkdirTemp("", "pi-web-access-proxy-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	headerFile := filepath.Join(dir, "headers")
	bodyFile := filepath.Join(dir, "body")
	requestBodyFile := filepath.Join(dir, "request-body")

	args := []string{
		"--silent",
		"--show-error",
		"--compressed",
		"--connect-timeout", "20",
		"-x", proxyURL,
		"-D", headerFile,
		"--output", bodyFile,
		"--write-out", "%{json}",
	}
	if method != "GET" && method != "HEAD" {
		args = append(args, "-X", method)
	}
	args = append(args, curlHeaderArgs(init.Header)...)
	if init.Body != nil {
		if err := os.WriteFile(requestBodyFile, init.Body, 0o600); err != nil {
			return nil, err
		}
		args = append(args, "--data-binary", "@"+requestBodyFile)
		if method == "GET" {
			args = append([]string{"-X", "GET"}, args...)
		}
	}
	args = append(args, u.String())

	res, err := runCurl(ctx, args)
	if err != nil {
		var aborted *abortedRequest
		if errors.As(err, &aborted) {
			return nil, err
		}
		return nil, fmt.Errorf("curl failed to start: %s", err.Error())
	}
	if ctx.Err() != nil {
		return nil, &abortedRequest{ctx.Err()}
	}
	if res.Code != 0 && jsTrim(res.Stdout) == "" {
		stderr := jsTrim(res.Stderr)
		suffix := ""
		if stderr != "" {
			suffix = ": " + stderr
		}
		return nil, &curlError{fmt.Sprintf("curl exited with code %d via %s%s", res.Code, RedactProxyURL(proxyURL), suffix)}
	}

	var dump []byte
	var body []byte
	// HEAD or empty responses may not produce output files.
	dump, _ = os.ReadFile(headerFile)
	if f, err := os.Open(bodyFile); err == nil {
		body, err = io.ReadAll(io.LimitReader(f, maxCurlBody+1))
		_ = f.Close()
		if len(body) > maxCurlBody {
			return nil, &curlError{fmt.Sprintf("Response too large (%dMB)", maxCurlBody>>20)}
		}
	}

	status, statusText, headers := parseHeaderDump(string(dump))
	if status == 0 {
		return nil, &curlError{fmt.Sprintf("Proxy fetch to %s via %s returned no HTTP status", u.String(), RedactProxyURL(proxyURL))}
	}

	finalURL := u
	if trimmed := jsTrim(res.Stdout); strings.HasPrefix(trimmed, "{") {
		var out struct {
			URLEffective string `json:"url_effective"`
		}
		// Older curl without %{json}: the header dump already provided the status.
		if json.Unmarshal([]byte(trimmed), &out) == nil && out.URLEffective != "" {
			if eff, err := url.Parse(out.URLEffective); err == nil {
				finalURL = eff
			}
		}
	}

	var respBody io.ReadCloser = io.NopCloser(bytes.NewReader(body))
	if status == 204 || status == 205 || status == 304 {
		respBody = http.NoBody
	}
	text := strconv.Itoa(status)
	if statusText != "" {
		text += " " + statusText
	}
	return &http.Response{
		Status:     text,
		StatusCode: status,
		Proto:      "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  headers,
		Body:    respBody,
		Request: &http.Request{Method: method, URL: finalURL, Header: init.Header},
	}, nil
}

// curlDoer sends provider API requests through curl while a proxy is scoped (utils.ts wraps the
// global fetch, which every provider call uses). Hosts that are bypassed go out directly.
type curlDoer struct {
	proxy  string
	direct HTTPDoer
}

func (d *curlDoer) Do(r *http.Request) (*http.Response, error) {
	if isProxyBypassedURL(r.URL) {
		return d.direct.Do(r)
	}
	var body []byte
	if r.Body != nil {
		b, err := io.ReadAll(r.Body)
		_ = r.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	return curlFetchOnce(r.Context(), r.URL, RequestInit{Method: r.Method, Header: r.Header, Body: body}, d.proxy)
}
