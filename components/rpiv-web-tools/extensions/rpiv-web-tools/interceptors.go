package rpiv_web_tools

import "context"

// Stub: the interceptor chain is not implemented yet.

type urlInterceptor interface {
	name() string
	intercept(ctx context.Context, url string, raw bool) *fetchResponse
}

func buildInterceptors(consumerGitHub *bool) []urlInterceptor { return nil }

func getInterceptors() []urlInterceptor { return nil }

func getActiveGitHubInterceptor() *githubInterceptor { return nil }

func clearCloneCache() {}

func resetWebToolsInterceptors() {}
