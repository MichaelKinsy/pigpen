package eq

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A host that exits during a run (a mutant that does not build, an extension that crashes PiG) must end
// the lane with an error event, never hang it. The driver's wait for a response and the shutdown both read
// the host's exit from one channel of capacity one; when the wait took the exit first, the shutdown waited
// 30 s and then blocked forever on the empty channel (review of the porter-driver lane: 14 of 40 lanes,
// and a `go test -race` run of this package, hung). The fake host reads the first request and exits, so
// its stdout closes and its exit arrives together.
func TestALaneWhoseHostExitsEarlyAlwaysReturns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake host is a POSIX shell script")
	}
	bin := filepath.Join(t.TempDir(), "pig")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nread x\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sc := &Scenario{Name: "exits", Steps: []Step{{RPC: map[string]any{"type": "get_state"}}}}
	const lanes = 24
	var wg sync.WaitGroup
	var returned, failed atomic.Int32
	for i := 0; i < lanes; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr, err := RunLane(sc, Lane{Name: "pig-go", Host: "pig", Bin: bin}, Options{StartupTimeout: 20 * time.Second})
			returned.Add(1)
			if err == nil {
				if _, bad := tr.Failed(); bad {
					failed.Add(1)
				}
			}
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		t.Fatalf("%d of %d lanes whose host exited early never returned", lanes-int(returned.Load()), lanes)
	}
	if failed.Load() != lanes {
		t.Errorf("%d of %d lanes recorded the early exit as a failure", failed.Load(), lanes)
	}
}
