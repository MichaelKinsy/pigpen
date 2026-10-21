package pi_background_tasks

import (
	"runtime"
	"strings"
	"testing"
	"time"
)

// A match that passes its deadline is abandoned by the caller; it must still end on regexp2's own timeout, not run on.
func TestATimedOutMatchLeavesNoGoroutineBehind(t *testing.T) {
	re, err := compileJSRegex(strings.Repeat("a", 1000)+"(a+)+$", "")
	if err != nil {
		t.Fatal(err)
	}
	baseline := runtime.NumGoroutine()
	_, timedOut := re.test(strings.Repeat("a", 1_000_000-1)+"!", 25*time.Millisecond)
	eq(t, timedOut, true, "the match timed out")
	waitUntil(t, func() bool { return runtime.NumGoroutine() <= baseline })
}
