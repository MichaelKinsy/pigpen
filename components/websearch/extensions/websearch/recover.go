package websearch

import "fmt"

// recoverInto is the one panic boundary for websearch's fan-out goroutines. Defer it first in the
// goroutine body, passing what to record when the goroutine panics:
//
//	go func(i int) {
//		defer recoverInto("fetch", func(err error) { results[i] = failure(err) })
//		...
//	}(i)
//
// The SDK recovers panics only on the request goroutine, and websearch runs in-process inside
// the user's agent (it is fused into the pig-with-batteries Binary), so a panic in a decoder or
// provider on a hostile page or response would otherwise end the whole session. recoverInto must
// be the deferred function itself: recover only works when called directly by a deferred call.
func recoverInto(what string, record func(err error)) {
	if r := recover(); r != nil {
		record(fmt.Errorf("%s failed unexpectedly: %v", what, r))
	}
}
