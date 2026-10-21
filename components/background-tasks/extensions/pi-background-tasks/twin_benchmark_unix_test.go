//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Twin of component-benchmark: a restored task with a 50 MB log, drawn through many frames of the dashboard. The original counts
// the operations its fixture sees (reads, opens, command wraps) and bounds the time of a step; the port counts the same on its
// reader (opens, reads, bytes) and on the wrap of the command.

func TestTwinComponentBenchmark(t *testing.T) {
	tw(t, "component-benchmark", "restored 50 MB dashboard frames bound log operations and command wrapping", func(t *testing.T) {
		const logBytes, tailChars = 50_000_000, 12_000
		dir := t.TempDir()
		logFile, secondLog := filepath.Join(dir, "restored.log"), filepath.Join(dir, "second.log")
		if err := os.WriteFile(logFile, []byte(strings.Repeat("a", logBytes)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(secondLog, []byte("second tail"), 0o644); err != nil {
			t.Fatal(err)
		}
		snapshot := func(id string, startedAt int, command, log string) map[string]any {
			return map[string]any{"id": id, "title": command, "command": command, "cwd": dir, "pid": 0, "status": "completed", "exitCode": 0, "startedAt": startedAt,
				"updatedAt": startedAt, "lastOutputAt": nil, "expiresAt": nil, "outputBytes": 0, "logFile": log, "notifyOnExit": false, "notifyOnOutput": false, "exitNotified": true}
		}
		tasks := []any{
			snapshot("bg-1", 2, "cache-me "+strings.Repeat("c", 8_000), logFile),
			snapshot("bg-2", 1, "cache-me second", secondLog),
		}
		branch := []map[string]any{{"type": "custom", "customType": "kendex-background-tasks:state", "data": map[string]any{"version": 1, "updatedAt": 3, "tasks": tasks}}}
		r := rigHost(t, rigOptions{config: map[string]any{"logTailMaxChars": tailChars}, branch: branch})
		r.rig.Settle()
		io := r.rig.CountLogIO()
		d := r.rig.OpenDashboard()
		defer d.Dispose()

		type phase struct {
			name                string
			frames, width       int
			change              func()
			reads, bytes, wraps int
		}
		rows := []phase{
			// Three UTF-8 bytes per UTF-16 unit, plus one for a split surrogate pair.
			{"steady", 30, 120, func() {}, 1, 36_001, 1},
			{"expanded", 10, 120, func() { d.Input("x") }, 0, 0, 0},
			{"width", 10, 80, func() {}, 0, 0, 1},
			{"content", 10, 80, func() { r.rig.AppendToCommand("bg-1", " changed") }, 0, 0, 1},
			{"theme", 10, 80, func() { d.Invalidate() }, 0, 0, 1},
			{"second", 1, 80, func() { d.Input(keyDown) }, 1, 11, 1},
			{"return", 1, 80, func() { d.Input(keyUp) }, 0, 0, 1},
		}
		for _, row := range rows {
			row.change()
			opens0, reads0, bytes0 := io.Counts()
			wraps0 := d.CommandWraps()
			var maxStep time.Duration
			for range row.frames {
				started := time.Now()
				if len(d.Render(row.width)) == 0 {
					t.Fatalf("%s: a frame draws lines", row.name)
				}
				// Real I/O must close before the next frame's cache hit is measured.
				d.WaitReads()
				maxStep = max(maxStep, time.Since(started))
			}
			opens, reads, bytes := io.Counts()
			got := fmt.Sprint(row.frames, opens-opens0, reads-reads0, bytes-bytes0, d.CommandWraps()-wraps0)
			want := fmt.Sprint(row.frames, row.frames, row.reads, row.bytes, row.wraps)
			if got != want {
				t.Errorf("component operations: %s (frames, opens, reads, bytes, wraps): got %s want %s", row.name, got, want)
			}
			// Each step must finish before the dashboard's one-second refresh interval.
			if maxStep >= time.Second {
				t.Errorf("component step: %s took %v", row.name, maxStep)
			}
		}
	})
	tskip(t, "component-benchmark", "must-fail: bypassing the command cache exceeds the benchmark operation bound", "a must-fail source mutation of the original; the Go mutant dashboard-command-cache-bypassed (port/mutations.json) must fail TestTwinComponentBenchmark")
}

// The terminal's bytes for the arrow keys (the original's fixture matches the key names themselves).
const (
	keyDown = "\x1b[B"
	keyUp   = "\x1b[A"
)
