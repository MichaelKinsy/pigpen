package ask_user_question_test

import (
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestPerfRPCWalker times the ask_user_question tool through the RPC dialog walker on the in-process fake host, for
// the performance section of the PR (same file, run against each release). It does nothing unless AUQ_PERF is set:
//
//	AUQ_PERF=1 go test -run TestPerfRPCWalker -count=1 .
//
// Each line is "PERF <input> ns/op=<n> B/op=<n> allocs/op=<n>", the mean of 300 calls after 30 warm-up calls.
func TestPerfRPCWalker(t *testing.T) {
	if os.Getenv("AUQ_PERF") == "" {
		t.Skip("set AUQ_PERF=1 to run")
	}
	inputs := []struct {
		name   string
		args   map[string]any
		script []reply
	}{
		{"single-select", singleArgs(), []reply{pick("1. A — a")}},
		{"three-questions", askArgs(
			questionArg("Which library should we use for dates?", "Library", false, optArg("date-fns", "Modular"), optArg("dayjs", "Tiny"), optArg("luxon", "Time zones")),
			questionArg("Which checks should run?", "Checks", true, optArg("lint", "Static"), optArg("unit", "Tests"), optArg("e2e", "End to end")),
			questionArg("Merge strategy?", "Merge", false, optArg("squash", "One commit"), optArg("rebase", "Linear")),
		), []reply{pick("2. dayjs — Tiny"), pick("1,3"), pick("1. squash — One commit")}},
	}
	const warm, runs = 30, 300
	for _, in := range inputs {
		script := make([]reply, 0, (warm+runs)*len(in.script))
		for i := 0; i < warm+runs; i++ {
			script = append(script, in.script...)
		}
		r := startRig(t, rpcOpts(), script...)
		for i := 0; i < warm; i++ {
			r.ask(in.args)
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		start := time.Now()
		for i := 0; i < runs; i++ {
			r.ask(in.args)
		}
		elapsed := time.Since(start)
		runtime.ReadMemStats(&after)
		fmt.Printf("PERF %s ns/op=%d B/op=%d allocs/op=%d\n", in.name, elapsed.Nanoseconds()/runs,
			(after.TotalAlloc-before.TotalAlloc)/runs, (after.Mallocs-before.Mallocs)/runs)
	}
}
