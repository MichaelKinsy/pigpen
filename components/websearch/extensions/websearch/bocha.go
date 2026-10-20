package websearch

import "context"

// Stub: the twins of this provider run against it and fail until the implementation lands.

func IsBochaAvailable() bool { return false }

func SearchWithBocha(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return nil, errNotPorted("bocha")
}
