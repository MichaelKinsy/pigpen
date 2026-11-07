package langsmith

// Measurement-only file (never shipped): port/perf/run.sh copies the extension sources into a scratch directory and
// adds this file to the same package. On SIGUSR2 it writes the Go runtime's memory counters to
// $LS_PERF_MEM_DIR/<pid>-<seq>.json, once as the process stands ("asis") and once after a forced collection ("gc").
// The driver reads /proc first and signals second. Goroutines excludes the reporter's own goroutine.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"syscall"
)

type perfMem struct {
	HeapInuse    uint64 `json:"heap_inuse"`
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapSys      uint64 `json:"heap_sys"`
	HeapObjects  uint64 `json:"heap_objects"`
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
	return perfMem{m.HeapInuse, m.HeapAlloc, m.HeapSys, m.HeapObjects, m.StackInuse, m.Sys, m.TotalAlloc, m.Mallocs, m.Frees, m.NumGC, m.PauseTotalNs, runtime.NumGoroutine() - 1}
}

// profiling: with LS_PERF_PPROF=<dir> the CPU profile runs from process start and SIGUSR1 stops it and writes
// <pid>.cpu.pprof, <pid>.allocs.pprof (alloc_space/alloc_objects since start) and <pid>.done in that directory.
func startProfiling() {
	dir := os.Getenv("LS_PERF_PPROF")
	if dir == "" {
		return
	}
	runtime.MemProfileRate = 4096
	cpu, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d.cpu.pprof", os.Getpid())))
	if err != nil || pprof.StartCPUProfile(cpu) != nil {
		return
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGUSR1)
	go func() {
		<-signals
		pprof.StopCPUProfile()
		cpu.Close()
		if f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d.allocs.pprof", os.Getpid()))); err == nil {
			_ = pprof.Lookup("allocs").WriteTo(f, 0)
			f.Close()
		}
		if f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d.done", os.Getpid()))); err == nil {
			f.Close()
		}
	}()
}

func init() {
	startProfiling()
	dir := os.Getenv("LS_PERF_MEM_DIR")
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
