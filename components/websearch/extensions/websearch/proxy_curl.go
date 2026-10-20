package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// Proxied requests run `curl`, as utils.ts fetchViaCurl does (red stub: see proxy_curl_test.go).

var errCurlStub = errors.New("curl transport is not implemented")

func curlFetchOnce(context.Context, *url.URL, RequestInit, string) (*http.Response, error) {
	return nil, errCurlStub
}

// isProxyBypassedURL is utils.ts isProxyBypassedUrl.
func isProxyBypassedURL(*url.URL) bool { return false }

// curlDoer sends provider API requests through curl while a proxy is scoped.
type curlDoer struct {
	proxy  string
	direct HTTPDoer
}

func (d *curlDoer) Do(*http.Request) (*http.Response, error) { return nil, errCurlStub }
