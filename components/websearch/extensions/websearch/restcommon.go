package websearch

import "time"

// Stub: the shared mechanics of the keyed REST providers land with the implementation.

var restTimeout = func(d time.Duration) time.Duration { return d }
