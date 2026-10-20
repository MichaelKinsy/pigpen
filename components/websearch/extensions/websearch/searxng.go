package websearch

import "context"

// Stub: the twins of this provider run against it and fail until the implementation lands.

func IsSearXNGAvailable() (bool, error) { return false, nil }

func SearchWithSearXNG(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return nil, errNotPorted("searxng")
}
