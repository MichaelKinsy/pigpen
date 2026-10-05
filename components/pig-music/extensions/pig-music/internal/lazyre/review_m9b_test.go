package lazyre

import (
	"sync"
	"testing"
)

// Review of M9b: package-level patterns are shared by every goroutine of the extension (the UI loop, enrichment, the doctor),
// so the first use may well be a concurrent one. Run with -race.
func TestReviewTheFirstUseMayBeConcurrent(t *testing.T) {
	for round := 0; round < 50; round++ {
		re := New(`^[a-z]+(\d+)$`)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if m := re.FindStringSubmatch("abc12"); len(m) != 2 || m[1] != "12" {
					t.Errorf("round %d: %v", round, m)
				}
			}()
		}
		close(start)
		wg.Wait()
	}
}
