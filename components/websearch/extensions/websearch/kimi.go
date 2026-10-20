package websearch

import "context"

// Stub of kimi-search.ts.

func SearchWithKimi(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errModelProviderNotPorted
}

func IsKimiSearchAvailable(ExtensionContext) bool { return false }
