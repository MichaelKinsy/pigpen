package websearch

import "context"

// Stub of xai-search.ts.

func SearchWithXai(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errModelProviderNotPorted
}

func IsXaiSearchAvailable(ExtensionContext) bool { return false }
