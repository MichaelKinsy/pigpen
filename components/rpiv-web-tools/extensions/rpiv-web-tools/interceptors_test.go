// SPDX-License-Identifier: MIT

package rpiv_web_tools

import "testing"

const fChain = "providers/interceptors/chain"

// countingInterceptor answers for exactly the URLs it owns and counts how often it was asked.
type countingInterceptor struct {
	name_ string
	urls  map[string]string
	asked []string
}

func (c *countingInterceptor) name() string { return c.name_ }

func (c *countingInterceptor) intercept(target string, _ bool) (fetchResponse, bool, error) {
	c.asked = append(c.asked, target)
	if body, ok := c.urls[target]; ok {
		return fetchResponse{Text: body}, true, nil
	}
	return fetchResponse{}, false, nil
}

func TestInterceptorChain(t *testing.T) {
	tw(t, fChain, "default OFF: github URL hits provider fetch when neither user nor consumer opts in", func(t *testing.T) {
		configHome(t)
		registry := &interceptorRegistry{}
		chain := registry.build(readUserGitHubConfig(), false)
		eq(t, len(chain), 0, "empty chain")
		eq(t, registry.activeGitHubInterceptor(), (*gitHubInterceptor)(nil), "no interceptor")

		client := &fakeHTTP{status: 200, contentType: "text/plain", body: "provider body"}
		res, err := fetchDispatch(client, chain, nil, "https://github.com/owner/repo", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "provider body", "the provider handled it")
		eq(t, client.calls, 1, "one generic request")
	})
	tw(t, fChain, "consumer:true enables the interceptor when user config is absent", func(t *testing.T) {
		configHome(t)
		registry := &interceptorRegistry{}
		chain := registry.build(readUserGitHubConfig(), true)
		eq(t, len(chain), 1, "one interceptor")
		if registry.activeGitHubInterceptor() == nil {
			t.Fatal("the GitHub interceptor must be installed")
		}
		eq(t, registry.activeGitHubInterceptor().options.Enabled, true, "enabled")
	})
	tw(t, fChain, "user config false beats consumer true (explicit user disable)", func(t *testing.T) {
		configHome(t)
		write, _ := configHome(t)
		write(`{"interceptors":{"github":false}}`)
		registry := &interceptorRegistry{}
		chain := registry.build(readUserGitHubConfig(), true)
		eq(t, len(chain), 0, "the explicit user disable wins")
		eq(t, registry.activeGitHubInterceptor(), (*gitHubInterceptor)(nil), "no interceptor")
	})
	tw(t, fChain, "user object form implies opt-in even when consumer left it unset", func(t *testing.T) {
		configHome(t)
		write, _ := configHome(t)
		write(`{"interceptors":{"github":{"clonePath":"/tmp/x"}}}`)
		registry := &interceptorRegistry{}
		chain := registry.build(readUserGitHubConfig(), false)
		eq(t, len(chain), 1, "the object form opts in")
		eq(t, registry.activeGitHubInterceptor().options.ClonePath, "/tmp/x", "its option applies")
	})
	tw(t, fChain, "interceptor returning null falls through to provider fetch", func(t *testing.T) {
		interceptor := &countingInterceptor{name_: "first", urls: map[string]string{}}
		providerCalls := 0
		provider := func(target string, _ bool) (fetchResponse, error) {
			providerCalls++
			return fetchResponse{Text: "provider: " + target}, nil
		}
		res, err := fetchDispatch(&fakeHTTP{status: 200, body: "generic"}, []urlInterceptor{interceptor}, provider,
			"https://example.com/x", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "provider: https://example.com/x", "the provider answered")
		eq(t, providerCalls, 1, "the provider was asked once")
		eq(t, len(interceptor.asked), 1, "the interceptor was asked once")
	})
	tw(t, fChain, "empty chain (interceptor disabled) is a no-op — every URL hits the provider", func(t *testing.T) {
		providerCalls := 0
		provider := func(_ string, _ bool) (fetchResponse, error) {
			providerCalls++
			return fetchResponse{Text: "provider"}, nil
		}
		for _, target := range []string{
			"https://github.com/owner/repo/blob/main/f.go",
			"https://gitlab.com/a/b",
			"https://example.com",
		} {
			if _, err := fetchDispatch(&fakeHTTP{status: 200, body: "generic"}, nil, provider, target, false); err != nil {
				t.Fatal(err)
			}
		}
		eq(t, providerCalls, 3, "every URL reached the provider")
	})
	t.Run("a claimed URL is answered by the first interceptor that owns it", func(t *testing.T) {
		first := &countingInterceptor{name_: "first", urls: map[string]string{"https://x/y": "first body"}}
		second := &countingInterceptor{name_: "second", urls: map[string]string{"https://x/y": "second body"}}
		res, err := fetchDispatch(&fakeHTTP{status: 200, body: "generic"}, []urlInterceptor{first, second}, nil, "https://x/y", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "first body", "the first owner wins")
		eq(t, len(second.asked), 0, "the chain stops at the first owner")
	})
	t.Run("a search-only provider with no key falls through to the generic path", func(t *testing.T) {
		// The provider returns nothing (no fetch arm), so the generic path answers instead of returning empty.
		client := &fakeHTTP{status: 200, contentType: "text/html", body: "<html><body><p>generic</p></body></html>"}
		res, err := fetchDispatch(client, nil, nil, "https://example.com", false)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, res.Text, "generic", "the generic path answered")
	})
	t.Run("reset clears the chain and the interceptor", func(t *testing.T) {
		registry := &interceptorRegistry{}
		registry.build(userGitHubConfig{Set: true}, false)
		registry.reset()
		eq(t, len(registry.interceptors()), 0, "chain cleared")
		eq(t, registry.activeGitHubInterceptor(), (*gitHubInterceptor)(nil), "interceptor cleared")
	})
}
