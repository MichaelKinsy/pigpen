package websearch

import "context"

// Stub of mistral-search.ts.

func SearchWithMistral(context.Context, string, SearchOptions) (*SearchResponse, error) {
	return nil, errModelProviderNotPorted
}

func IsMistralAvailable() bool { return false }
