package tintinweb_tasks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskPaths(t *testing.T) {
	const f = "task-paths"
	tw(t, f, "names a workspace the way pi names it in its own session logs", func(t *testing.T) {
		// pi writes transcripts to <agent-dir>/sessions/--Users-me-work-repo--/, so
		// a workspace's tasks and its transcripts are found under the same name.
		eq(t, projectKey("/Users/me/work/repo"), "--Users-me-work-repo--")
		eq(t, projectKey("/mnt/c:/work/repo"), "--mnt-c--work-repo--") // a drive colon is a separator too
	})
	tw(t, f, "keeps different workspaces apart", func(t *testing.T) {
		if projectKey("/Users/me/a") == projectKey("/Users/me/b") {
			t.Fatal("two workspaces share a key")
		}
	})
	tw(t, f, "resolves a relative workspace against the process directory", func(t *testing.T) {
		wd, _ := os.Getwd()
		eq(t, projectKey("."), projectKey(wd))
	})
	tw(t, f, "writes into the workspace, exactly where every release so far has", func(t *testing.T) {
		// The compatibility guarantee: upgrading must not move anyone's tasks. Asserted literally.
		eq(t, sessionTaskFile("/Users/me/work/repo", "abc", "session"),
			filepath.Join("/Users/me/work/repo", ".pi", "tasks", "tasks-abc.json"))
	})
	tw(t, f, "ignores the agent directory entirely", func(t *testing.T) {
		cwd := scratch(t)
		if strings.HasPrefix(sessionTaskFile(cwd, "abc", "session"), agentDir()) {
			t.Fatal("session scope wrote under the agent directory")
		}
	})
	tw(t, f, "keeps a new session's tasks outside the workspace", func(t *testing.T) {
		cwd := scratch(t)
		file := sessionTaskFile(cwd, "abc", "session-global")
		eq(t, file, filepath.Join(agentDir(), "tasks", "sessions", projectKey(cwd), "tasks-abc.json"))
		if strings.HasPrefix(file, cwd) {
			t.Fatal("a global session file is inside the workspace")
		}
	})
	tw(t, f, "leaves a session that already has a workspace file where it is", func(t *testing.T) {
		// Opting in changes where new files are created; it does not move data.
		cwd := scratch(t)
		existing := filepath.Join(cwd, ".pi", "tasks", "tasks-abc.json")
		writeJSON(t, existing, obj{"nextId": 1, "tasks": arr{}})
		eq(t, sessionTaskFile(cwd, "abc", "session-global"), existing)
	})
	tw(t, f, "does not confuse one session's workspace file for another's", func(t *testing.T) {
		cwd := scratch(t)
		writeJSON(t, filepath.Join(cwd, ".pi", "tasks", "tasks-abc.json"), obj{"nextId": 1, "tasks": arr{}})
		eq(t, sessionTaskFile(cwd, "xyz", "session-global"), filepath.Join(globalSessionTasksDir(cwd), "tasks-xyz.json"))
	})
	tw(t, f, "follows a relocated agent directory", func(t *testing.T) {
		// pi honours PI_CODING_AGENT_DIR for its own state; PiG honours PIG_CODING_AGENT_DIR (divergence D2).
		relocated, cwd := scratch(t), scratch(t)
		t.Setenv("PIG_CODING_AGENT_DIR", relocated)
		eq(t, sessionTaskFile(cwd, "abc", "session-global"),
			filepath.Join(relocated, "tasks", "sessions", projectKey(cwd), "tasks-abc.json"))
	})
	tw(t, f, "keeps sessions in one workspace together", func(t *testing.T) {
		cwd := scratch(t)
		for _, id := range []string{"a", "b"} {
			if !strings.HasPrefix(sessionTaskFile(cwd, id, "session-global"), globalSessionTasksDir(cwd)) {
				t.Fatalf("session %s is not under the workspace's global directory", id)
			}
		}
	})
}
