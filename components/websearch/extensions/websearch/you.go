package websearch

import "context"

// Stub: the twins of this provider run against it and fail until the implementation lands.

func IsYouAvailable() bool { return false }

func SearchWithYou(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return nil, errNotPorted("you")
}
