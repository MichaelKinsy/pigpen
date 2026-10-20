package websearch

import "context"

// Stub: the twins of this provider run against it and fail until the implementation lands.

func IsSerpBaseAvailable() bool { return false }

func SearchWithSerpBase(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return nil, errNotPorted("serpbase")
}
