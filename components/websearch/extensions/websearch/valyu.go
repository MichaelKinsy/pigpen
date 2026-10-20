package websearch

import (
	"context"
	"time"
)

// Stub: the twins of this provider run against it and fail until the implementation lands.

func IsValyuAvailable() bool { return false }

func SearchWithValyu(ctx context.Context, query string, o SearchOptions) (*SearchResponse, error) {
	return nil, errNotPorted("valyu")
}

var restNow = time.Now
