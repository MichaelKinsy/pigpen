// SPDX-License-Identifier: MIT

package rpiv_web_tools

// The interceptor chain and the fetch dispatch. upstream: providers/interceptors/index.ts buildInterceptors,
// getInterceptors, getActiveGitHubInterceptor, and the fetch dispatch in web-tools.ts registerWebFetchTool.
//
// The dispatch order is the contract: every interceptor is asked first, cheapest first, and a null answer means the
// next one gets a look; when none owns the URL the provider's own fetch() runs, and failing that the generic HTML
// path is the last resort.

// urlInterceptor is the specialist contract: it inspects a fetch target before the provider's fetch() runs, answers
// with a response when it owns the URL, and nil otherwise. upstream: providers/interceptors/types.ts UrlInterceptor.
type urlInterceptor interface {
	name() string
	intercept(target string, raw bool) (fetchResponse, bool, error)
}

// interceptorRegistry is the package-private state the orchestrator reads per request rather than caching the slice
// reference, so a re-registration cannot leave a stale chain behind. upstream: the activeInterceptors module state in
// providers/interceptors/index.ts.
type interceptorRegistry struct {
	active []urlInterceptor
	gitHub *gitHubInterceptor
}

// buildInterceptors resolves the opt-in and installs the chain: the GitHub interceptor when enabled, an empty chain
// when not. upstream: providers/interceptors/index.ts buildInterceptors.
func (r *interceptorRegistry) build(user userGitHubConfig, consumerDefault bool) []urlInterceptor {
	resolved := resolveGitHubOptions(user, consumerDefault)
	r.gitHub = nil
	if resolved.Enabled {
		r.gitHub = &gitHubInterceptor{options: resolved}
		r.active = []urlInterceptor{r.gitHub}
	} else {
		r.active = []urlInterceptor{}
	}
	return r.active
}

// interceptors is the installed chain. upstream: providers/interceptors/index.ts getInterceptors.
func (r *interceptorRegistry) interceptors() []urlInterceptor { return r.active }

// activeGitHubInterceptor is the GitHub specialist, or nil when the interceptor is off. upstream:
// providers/interceptors/index.ts getActiveGitHubInterceptor.
func (r *interceptorRegistry) activeGitHubInterceptor() *gitHubInterceptor { return r.gitHub }

// reset clears both the interceptor state and the chain, so the next registration starts from scratch. upstream:
// providers/interceptors/index.ts __resetWebToolsInterceptors.
func (r *interceptorRegistry) reset() {
	r.gitHub = nil
	r.active = nil
}

// gitHubInterceptor owns github.com code URLs. The clone and API halves land with slice 6b; until then it answers
// nil for everything, which is exactly what the chain contract asks of an interceptor that does not own the URL.
type gitHubInterceptor struct {
	options resolvedGitHubOptions
	// owned reports whether the interceptor claims a URL at all, which the slice 6b paths fill in.
	owned map[string]bool
}

func (g *gitHubInterceptor) name() string { return "github" }

// intercept claims the URL when the interceptor is enabled, the host is github.com and the path is code rather than
// one of the site's own pages. The response body comes with slice 6b; what is decided here is ownership, which is what
// the chain and the --show lines already depend on. upstream: providers/interceptors/github.ts GitHubInterceptor.intercept.
func (g *gitHubInterceptor) intercept(target string, _ bool) (fetchResponse, bool, error) {
	if !g.options.Enabled {
		return fetchResponse{}, false, nil
	}
	if _, ok := parseGitHubURL(target); !ok {
		return fetchResponse{}, false, nil
	}
	if g.owned == nil {
		return fetchResponse{}, false, nil
	}
	if !g.owned[target] {
		return fetchResponse{}, false, nil
	}
	return fetchResponse{}, false, errNotYetPorted
}

// errNotYetPorted is what the GitHub interceptor answers once a URL is claimed: the clone and API halves arrive with
// slice 6b. It is loud on purpose, so a claimed URL never falls through to a provider that would fetch the HTML page
// instead of the code.
var errNotYetPorted error = &unknownProviderError{Name: "github interceptor (clone and API paths, slice 6b)", Valid: nil}

// fetchDispatch is the three-stage resolution: the interceptor chain, then the provider's own fetch(), then the generic
// HTML path. providerFetch is nil for a search-only provider. upstream: the interceptor loop and the two fallbacks in
// registerWebFetchTool.
func fetchDispatch(client httpDoer, chain []urlInterceptor, providerFetch func(target string, raw bool) (fetchResponse, error), target string, raw bool) (fetchResponse, error) {
	for _, interceptor := range chain {
		res, ok, err := interceptor.intercept(target, raw)
		if err != nil {
			return fetchResponse{}, err
		}
		if ok {
			return res, nil
		}
	}
	if providerFetch != nil {
		res, err := providerFetch(target, raw)
		if err != nil {
			return fetchResponse{}, err
		}
		if res.Text != "" {
			return res, nil
		}
	}
	return fetchViaGenericHTML(client, target, raw)
}
