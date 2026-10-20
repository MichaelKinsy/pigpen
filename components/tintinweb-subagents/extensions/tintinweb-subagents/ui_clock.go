package tintinweb_subagents

import (
	"sync"
	"time"
)

// nowMs is Date.now(); the surfaces read the clock through it so a test can fix it.
var nowMs = func() int64 { return time.Now().UnixMilli() }

// startTicker runs f every d until the returned stop is called (a test replaces it with a manual clock).
// upstream: the setInterval of the widget (80 ms) and the fleet list (200 ms).
var startTicker = func(d time.Duration, f func()) (stop func()) {
	t := time.NewTicker(d)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-t.C:
				f()
			case <-done:
				return
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { t.Stop(); close(done) }) }
}
