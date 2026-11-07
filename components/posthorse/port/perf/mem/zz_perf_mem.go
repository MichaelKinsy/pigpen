package posthorse

// Measurement-only file: port/perf/run.sh copies the extension sources into a scratch directory and adds
// this file there. It is never part of the shipped package. On SIGUSR2 it writes the Go runtime's memory
// counters to $PH_PERF_MEM_DIR/<pid>-<seq>.json, once as the process stands ("asis") and once after a
// forced collection ("gc"). The driver reads RSS/PSS from /proc before it sends the signal.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"
)

type perfMem struct {
	HeapInuse    uint64 `json:"heap_inuse"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	StackInuse   uint64 `json:"stack_inuse"`
	Sys          uint64 `json:"sys"`
	TotalAlloc   uint64 `json:"total_alloc"`
	Mallocs      uint64 `json:"mallocs"`
	Frees        uint64 `json:"frees"`
	NumGC        uint32 `json:"num_gc"`
	PauseTotalNs uint64 `json:"pause_total_ns"`
	Goroutines   int    `json:"goroutines"`
}

func perfRead() perfMem {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return perfMem{m.HeapInuse, m.HeapAlloc, m.HeapSys, m.StackInuse, m.Sys, m.TotalAlloc, m.Mallocs, m.Frees, m.NumGC, m.PauseTotalNs, runtime.NumGoroutine()}
}

func init() {
	dir := os.Getenv("PH_PERF_MEM_DIR")
	if dir == "" {
		return
	}
	signals := make(chan os.Signal, 4)
	signal.Notify(signals, syscall.SIGUSR2)
	go func() {
		for seq := 0; ; seq++ {
			<-signals
			asis := perfRead()
			runtime.GC()
			debug.FreeOSMemory()
			after := perfRead()
			data, err := json.Marshal(map[string]any{"runtime": "go", "version": runtime.Version(), "gomaxprocs": runtime.GOMAXPROCS(0), "asis": asis, "gc": after})
			if err != nil {
				continue
			}
			name := filepath.Join(dir, fmt.Sprintf("%d-%d.json", os.Getpid(), seq))
			if os.WriteFile(name+".tmp", data, 0o600) == nil {
				_ = os.Rename(name+".tmp", name)
			}
		}
	}()
}
