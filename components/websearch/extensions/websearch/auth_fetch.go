package websearch

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// AuthFetchProfile is one authFetch profile of web-search.json (auth-fetch.ts).
type AuthFetchProfile struct {
	Name          string
	Hosts         []string
	ChromeProfile string
	Redirects     string
	Cache         string
}

var errAuthFetchStub = errors.New("authenticated fetch is not implemented")

// ResolveAuthFetchProfile is resolveAuthFetchProfile.
func ResolveAuthFetchProfile(AuthSpec) (AuthFetchProfile, error) {
	return AuthFetchProfile{}, errAuthFetchStub
}

// AssertAuthFetchURL is assertAuthFetchUrl.
func AssertAuthFetchURL(AuthFetchProfile, string) (*url.URL, error) { return nil, errAuthFetchStub }

// AuthFetchRedirectGuard is authFetchRedirectGuard.
func AuthFetchRedirectGuard(AuthFetchProfile, *url.URL, *url.URL) error { return errAuthFetchStub }

// authFetchFunc is the transport of an authenticated fetch.
func authFetchFunc(AuthFetchProfile, FetchFunc, ValidationOptions) FetchFunc {
	return func(context.Context, *url.URL, RequestInit) (*http.Response, error) { return nil, errAuthFetchStub }
}
