//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// Process-tree cleanup (slice 4): stopping a task, and shutting the session down, end the whole tree the task started, not only the
// shell that leads it. The task runs in its own session and process group, and a signal to the group reaches every descendant; a
// descendant that ignores SIGTERM is ended by the SIGKILL that follows after the grace period. The test runs unchanged on Linux and
// on macOS (it is compiled with `go test -c` for each and run as a binary).

func TestProcessTreeCleanup(t *testing.T) {
	rows := []struct {
		name, end string
		// script starts a tree of three descendants (two of them below a subshell) and records their pids.
		ignoreTerm bool
	}{
		{"stop ends the whole tree", "stop", false},
		{"shutdown ends the whole tree", "shutdown", false},
		{"stop ends descendants that ignore SIGTERM", "stop", true},
		{"shutdown ends descendants that ignore SIGTERM", "shutdown", true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			h := started(t, map[string]any{"forceKillGraceMs": 300})
			pids := filepath.Join(t.TempDir(), "pids")
			trap := ""
			if row.ignoreTerm {
				trap = "trap '' TERM; "
			}
			script := fmt.Sprintf("%ssleep 300 & echo $! >> %[2]s; (sleep 300 & echo $! >> %[2]s; wait) & echo $! >> %[2]s; wait", trap, pids)
			raw, failure := h.Tool("bg_task", map[string]any{"action": "spawn", "command": script, "notifyOnExit": false})
			eq(t, failure, "", "spawn")
			details, _ := text(t, raw).Details["task"].(map[string]any)
			leader, _ := details["pid"].(float64)
			if leader == 0 {
				t.Fatalf("no pid in %v", details)
			}
			var tree []int
			eventually(t, "the tree to be running", func() bool {
				b, err := os.ReadFile(pids)
				if err != nil {
					return false
				}
				tree = tree[:0]
				for _, line := range strings.Fields(string(b)) {
					if pid, err := strconv.Atoi(line); err == nil {
						tree = append(tree, pid)
					}
				}
				return len(tree) == 3
			})
			tree = append(tree, int(leader))
			for _, pid := range tree {
				if syscall.Kill(pid, 0) != nil {
					t.Fatalf("pid %d is not running before the end", pid)
				}
			}
			if row.end == "stop" {
				_, failure := h.Tool("bg_task", map[string]any{"action": "stop", "id": "bg-1"})
				eq(t, failure, "", "stop")
			} else {
				h.Fire("session_shutdown", map[string]any{"reason": "quit"})
			}
			eventually(t, "every process of the tree to be gone", func() bool {
				for _, pid := range tree {
					if syscall.Kill(pid, 0) == nil {
						return false
					}
				}
				return true
			})
		})
	}
}
