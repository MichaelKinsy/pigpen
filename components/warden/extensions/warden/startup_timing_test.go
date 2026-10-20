package warden

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"
)

// TestStartupTiming measures the time from constructing the extension to its registration reaching the host (the
// fake host, so no process start): run with WARDEN_STARTUP_TIMING=1 (port/bench/startup.sh interleaves it with the
// previous release). It prints the median and the best of 30 starts.
func TestStartupTiming(t *testing.T) {
	if os.Getenv("WARDEN_STARTUP_TIMING") == "" {
		t.Skip("set WARDEN_STARTUP_TIMING=1")
	}
	var samples []time.Duration
	for range 30 {
		start := time.Now()
		StartHost(t, New(Options{}), HostOptions{})
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	fmt.Printf("startup median %d us best %d us\n", samples[len(samples)/2].Microseconds(), samples[0].Microseconds())
}
