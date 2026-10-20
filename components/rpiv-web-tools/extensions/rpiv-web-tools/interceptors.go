package rpiv_web_tools

import (
	"context"
	"sync"
)

// The interceptor chain and its registry. upstream: providers/interceptors/index.ts, types.ts.
//
// A urlInterceptor inspects a fetch target before the provider's fetch runs: it returns a response when it
// owns the URL and nil otherwise, and the next interceptor (and at last the provider) gets the URL.

type urlInterceptor interface {
	name() string
	intercept(ctx context.Context, url string, raw bool) *fetchResponse
}

// The active chain is process state set when the extension registers, as the original's module singletons
// are: the tool reads it per request, and the config read at registration decides it.
var (
	interceptorMu      sync.Mutex
	activeInterceptors []urlInterceptor
	activeGitHub       *githubInterceptor
)

// buildInterceptors resolves the opt-in (the user's config over consumerGitHub, the embedding program's
// default; nil means none) and installs the chain. upstream: index.ts:28-40.
func buildInterceptors(consumerGitHub *bool) []urlInterceptor {
	resolved := resolveGitHubOptions(readUserGitHubConfig(readConfig()), consumerGitHub)
	interceptorMu.Lock()
	defer interceptorMu.Unlock()
	if resolved.Enabled {
		activeGitHub = newGitHubInterceptor(resolved.asUser())
		activeInterceptors = []urlInterceptor{activeGitHub}
	} else {
		activeGitHub = nil
		activeInterceptors = nil
	}
	return activeInterceptors
}

func getInterceptors() []urlInterceptor {
	interceptorMu.Lock()
	defer interceptorMu.Unlock()
	return activeInterceptors
}

func getActiveGitHubInterceptor() *githubInterceptor {
	interceptorMu.Lock()
	defer interceptorMu.Unlock()
	return activeGitHub
}

// clearCloneCache resets the active GitHub interceptor, removing its clones.
func clearCloneCache() {
	if g := getActiveGitHubInterceptor(); g != nil {
		g.reset()
	}
}

// resetWebToolsInterceptors resets the active interceptor and drops the chain.
func resetWebToolsInterceptors() {
	interceptorMu.Lock()
	g := activeGitHub
	activeGitHub = nil
	activeInterceptors = nil
	interceptorMu.Unlock()
	if g != nil {
		g.reset()
	}
}
