//go:build !windows

package pi_background_tasks_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	bg "github.com/MichaelKinsy/pigpen/pi-background-tasks"
)

// Twin of task-retention.test.ts: real children, the real lane directory, no fakes beyond the Rig's timers.

func dirNames(t *testing.T, dir string, keep func(string) bool) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, e := range entries {
		if keep == nil || keep(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

func listedCount(t *testing.T, out toolOut) int {
	t.Helper()
	switch tasks := out.Details["tasks"].(type) {
	case []any:
		return len(tasks)
	case map[string]any:
		counts, _ := tasks["counts"].(map[string]any)
		n, _ := counts["tasks"].(float64)
		return int(n)
	}
	counts, _ := out.Details["counts"].(map[string]any)
	n, _ := counts["tasks"].(float64)
	return int(n)
}

func TestTwinTaskRetention(t *testing.T) {
	tw(t, "task-retention", "task logs follow the lane retention rule, and finished tasks are bounded and released", func(t *testing.T) {
		// A small output buffer, so a finished task's log is longer than what is read back.
		r := rigHost(t, rigOptions{config: map[string]any{"outputBufferMaxChars": 64}, session: "retention-session", noStart: true})
		cwd := r.opts.Cwd
		taskDir := os.Getenv("PI_BG_TASK_DIR")
		lanes := filepath.Join(taskDir, "lanes")
		past := time.Now().Add(-5*24*time.Hour - time.Minute)
		must := func(err error) {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
		}
		write := func(path, text string) {
			must(os.MkdirAll(filepath.Dir(path), 0o755))
			must(os.WriteFile(path, []byte(text), 0o644))
		}
		// Two lanes a previous run left (a worktree that is gone, a log past five days), and three folders the package did not
		// make: one outside the lanes folder, one in it with no lane record, one a symbolic link out of the task directory.
		goneLane := filepath.Join(lanes, "gone-lane")
		write(filepath.Join(goneLane, ".lane-cwd"), filepath.Join(cwd, "removed-worktree"))
		write(filepath.Join(goneLane, "bg-1-1.log"), "gone")
		oldLane := filepath.Join(lanes, "old-lane")
		write(filepath.Join(oldLane, ".lane-cwd"), cwd)
		write(filepath.Join(oldLane, "bg-1-1.log"), "old")
		write(filepath.Join(oldLane, "bg-2-2.log"), "fresh")
		must(os.Chtimes(filepath.Join(oldLane, "bg-1-1.log"), past, past))
		foreign := filepath.Join(taskDir, "foreign")
		write(filepath.Join(foreign, ".lane-cwd"), filepath.Join(cwd, "removed-worktree"))
		write(filepath.Join(foreign, "old.txt"), "foreign")
		unmarked := filepath.Join(lanes, "unmarked")
		write(filepath.Join(unmarked, "old.log"), "unmarked")
		must(os.Chtimes(filepath.Join(unmarked, "old.log"), past, past))
		victim := filepath.Join(cwd, "victim")
		write(filepath.Join(victim, ".lane-cwd"), cwd)
		write(filepath.Join(victim, "old.txt"), "victim")
		must(os.Chtimes(filepath.Join(victim, "old.txt"), past, past))
		must(os.Symlink(victim, filepath.Join(lanes, "planted")))

		r.Fire("session_start", map[string]any{"reason": "startup"})
		_, goneErr := os.Stat(goneLane)
		eq(t, os.IsNotExist(goneErr), true, "the lane whose worktree is gone is removed")
		eq(t, dirNames(t, oldLane, nil), []string{".lane-cwd", "bg-2-2.log"}, "the log past five days is removed")
		eq(t, dirNames(t, foreign, nil), []string{".lane-cwd", "old.txt"}, "a folder the package did not make keeps its files")
		eq(t, dirNames(t, unmarked, nil), []string{"old.log"}, "a lane folder without a record keeps its files")
		eq(t, dirNames(t, victim, nil), []string{".lane-cwd", "old.txt"}, "a link out of the task directory is not followed")

		waitExit := func(id string) {
			r.waitStatus(t, id, "completed")
			r.rig.DrainLogs()
			r.rig.Settle()
		}
		spawned := bg.MaxFinishedTasks + 5
		for i := 1; i <= spawned; i++ {
			_, failure := r.Tool("bg_task", map[string]any{"action": "spawn", "command": fmt.Sprintf("printf out-%d", i), "notifyOnExit": false})
			eq(t, failure, "", "spawn")
			if i <= bg.MaxFinishedTasks || true {
				waitFor(t, "bg-"+fmt.Sprint(i)+" to finish or to be forgotten", func() bool {
					s := r.status(fmt.Sprintf("bg-%d", i))
					return s == "completed"
				})
				r.rig.DrainLogs()
				r.rig.Settle()
			}
		}
		raw, _ := r.Tool("bg_task", map[string]any{"action": "list"})
		eq(t, listedCount(t, text(t, raw)), bg.MaxFinishedTasks, "listed")
		raw, _ = r.Tool("bg_task", map[string]any{"action": "log", "id": fmt.Sprintf("bg-%d", spawned)})
		newest := text(t, raw).Details["task"].(map[string]any)
		laneDir := filepath.Join(lanes, "retention-session")
		isLog := func(n string) bool { return strings.HasSuffix(n, ".log") }
		logFile := newest["logFile"].(string)
		eq(t, strings.HasPrefix(logFile, laneDir+string(filepath.Separator)), true, "the log is in the session's lane")
		_, laneCwdErr := os.Stat(filepath.Join(laneDir, ".lane-cwd"))
		eq(t, laneCwdErr, nil, "the lane has its record")
		eq(t, len(dirNames(t, laneDir, isLog)), bg.MaxFinishedTasks, "logs before clear")
		// A finished task's output is read from its log: with the log gone, nothing is left in memory.
		must(os.Remove(logFile))
		raw, _ = r.Tool("bg_task", map[string]any{"action": "log", "id": newest["id"].(string)})
		eq(t, text(t, raw).text(), "(empty)", "the newest log with its file gone")
		r.Tool("bg_task", map[string]any{"action": "clear"})
		waitFor(t, "the logs to be removed", func() bool { r.rig.DrainLogs(); return len(dirNames(t, laneDir, isLog)) == 0 })

		// A finished task's output is the end of its log, not the start.
		raw, _ = r.Tool("bg_task", map[string]any{"action": "spawn", "command": "printf HEAD; printf '%0200d' 0; printf TAIL-END", "notifyOnExit": false})
		long := text(t, raw).Details["task"].(map[string]any)
		waitExit(long["id"].(string))
		raw, _ = r.Tool("bg_task", map[string]any{"action": "log", "id": long["id"].(string)})
		longLog := text(t, raw).text()
		eq(t, []bool{strings.Contains(longLog, "TAIL-END"), strings.Contains(longLog, "HEAD")}, []bool{true, false}, "long log: tail, head")

		// A log that stopped taking writes leaves the output in memory as the record.
		raw, _ = r.Tool("bg_task", map[string]any{"action": "spawn", "command": "sleep 0.3; printf late-output", "notifyOnExit": false})
		unlogged := text(t, raw).Details["task"].(map[string]any)
		unloggedFile := unlogged["logFile"].(string)
		must(os.Remove(unloggedFile))
		must(os.Mkdir(unloggedFile, 0o755))
		waitExit(unlogged["id"].(string))
		raw, _ = r.Tool("bg_task", map[string]any{"action": "log", "id": unlogged["id"].(string)})
		eq(t, text(t, raw).text(), "late-output", "unlogged log")

		raw, _ = r.Tool("bg_task", map[string]any{"action": "log", "id": long["id"].(string)})
		parent := text(t, raw).Details["task"].(map[string]any)

		r.Fire("session_shutdown", map[string]any{"reason": "quit"})
		raw, _ = r.Tool("bg_task", map[string]any{"action": "list"})
		eq(t, text(t, raw).text(), "No background tasks.", "listed after shutdown")

		// A fork copies the branch, so it restores this session's finished tasks: one past the bound, each with its log in this
		// session's lane. The bound at session_start and a clear in the fork forget them, and the logs stay.
		var branch []map[string]any
		var parentLogs []string
		for i := 0; i <= bg.MaxFinishedTasks; i++ {
			file := filepath.Join(laneDir, fmt.Sprintf("bg-%d-parent.log", 100+i))
			must(os.WriteFile(file, []byte("parent-output"), 0o644))
			parentLogs = append(parentLogs, file)
			task := map[string]any{}
			for k, v := range parent {
				task[k] = v
			}
			task["id"], task["logFile"], task["updatedAt"] = fmt.Sprintf("bg-%d", 100+i), file, parent["updatedAt"].(float64)+float64(i)
			branch = append(branch, map[string]any{"type": "message", "message": map[string]any{"role": "toolResult", "toolName": "bg_task", "details": map[string]any{"action": "log", "task": task}}})
		}
		r.setSession("fork-session", branch)
		r.Fire("session_start", map[string]any{"reason": "fork"})
		raw, _ = r.Tool("bg_task", map[string]any{"action": "list"})
		forkListed := listedCount(t, text(t, raw))
		r.Tool("bg_task", map[string]any{"action": "clear"})
		kept := 0
		for _, f := range parentLogs {
			if _, err := os.Stat(f); err == nil {
				kept++
			}
		}
		raw, _ = r.Tool("bg_task", map[string]any{"action": "list"})
		eq(t, []any{parent["sessionId"], forkListed, kept, text(t, raw).text()}, []any{"retention-session", bg.MaxFinishedTasks, bg.MaxFinishedTasks + 1, "No background tasks."}, "fork")
	})
}
