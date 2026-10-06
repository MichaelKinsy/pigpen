// SPDX-License-Identifier: MIT

package rpiv_web_tools

import (
	"fmt"
	"os"
)

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
		r.gitHub = newGitHubInterceptor(resolved)
		r.active = []urlInterceptor{r.gitHub}
	} else {
		r.active = []urlInterceptor{}
	}
	return r.active
}

// newGitHubInterceptor is the production interceptor: the exec runner, the clone renderer and the stderr hint. upstream:
// the GitHubInterceptor construction in buildInterceptors.
func newGitHubInterceptor(options resolvedGitHubOptions) *gitHubInterceptor {
	return &gitHubInterceptor{
		options:    options,
		runner:     execRunner{},
		cloneCache: map[string]cachedClone{},
		generate:   generateCloneContent,
		warn:       func(message string) { fmt.Fprintln(os.Stderr, message) },
	}
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

// gitHubInterceptor owns github.com code URLs: it decides ownership, then either clones the repository or answers from
// the API. upstream: providers/interceptors/github.ts GitHubInterceptor.
type gitHubInterceptor struct {
	options resolvedGitHubOptions
	// runner is the seam for gh and git, so the decision path is testable without either tool installed.
	runner githubRunner
	// cloneCache holds the finished or in-flight clones, keyed by owner, repo and ref. upstream: the cloneCache map.
	cloneCache map[string]cachedClone
	// ghProbed and ghPresent cache the one-time gh probe; ghHintShown keeps the install hint to one line per process.
	ghProbed, ghPresent, ghHintShown bool
	// forceClone skips the size check, and aborted short-circuits every branch, both standing in for the caller's
	// request state.
	forceClone, aborted bool
	// generate renders a clone; the field keeps the rendering seam explicit and stubbable.
	generate func(localPath string, info gitHubURLInfo) string
	// warn receives the one-time gh hint.
	warn func(string)
}

func (g *gitHubInterceptor) name() string { return "github" }

// intercept claims the URL when the interceptor is enabled, the host is github.com and the path is code rather than
// one of the site's own pages, then answers it from the clone or the API. upstream: GitHubInterceptor.intercept.
func (g *gitHubInterceptor) intercept(target string, raw bool) (fetchResponse, bool, error) {
	return g.interceptFetch(target, nil)
}

// reset clears the clone cache, removes the cloned directories and forgets the gh probe, so the next intercept
// re-checks. upstream: GitHubInterceptor.reset.
func (g *gitHubInterceptor) reset() {
	for _, entry := range g.cloneCache {
		_ = removeAll(entry.localPath)
	}
	g.cloneCache = nil
	g.ghProbed, g.ghPresent, g.ghHintShown = false, false, false
}

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
