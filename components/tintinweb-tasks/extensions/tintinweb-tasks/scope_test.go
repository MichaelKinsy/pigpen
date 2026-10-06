package tintinweb_tasks_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storedSubjects reads the subjects of a task file directly.
func storedSubjects(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Tasks []struct {
			Subject string `json:"subject"`
		} `json:"tasks"`
	}
	if err := jsonUnmarshal(data, &d); err != nil {
		t.Fatal(err)
	}
	out := []string{}
	for _, tk := range d.Tasks {
		out = append(out, tk.Subject)
	}
	return out
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// seedStore writes a session task file holding tasks in the given states ("pending" or "completed").
func seedStore(t *testing.T, path string, statuses ...string) {
	t.Helper()
	tasks := arr{}
	for i, s := range statuses {
		n := i + 1
		tasks = append(tasks, obj{"id": itoa(n), "subject": "Task " + itoa(n), "description": "d", "status": s,
			"metadata": obj{}, "blocks": arr{}, "blockedBy": arr{}, "createdAt": 1, "updatedAt": 1})
	}
	writeConfig(t, path, obj{"nextId": len(statuses) + 1, "tasks": tasks})
}

func TestStoreScope(t *testing.T) {
	const f = "store-scope"
	start := func(t *testing.T, reason string) (*rig, string) {
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": reason})
		return r, cwd
	}
	tw(t, f, "persists to a single shared file", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "project"})
		useEnv(t, "PI_TASKS", "")
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Shared", "description": "d"})
		project := filepath.Join(cwd, ".pi", "tasks", "tasks.json")
		eqv(t, exists(project), true)
		eqv(t, storedSubjects(t, project), []string{"Shared"})
	})
	tw(t, f, "stays on the same file when the session changes", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "project"})
		useEnv(t, "PI_TASKS", "")
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Before", "description": "d"})
		r.setSession("s2", true)
		r.fireEvent("session_start", obj{"reason": "new"})
		r.must("TaskCreate", obj{"subject": "After", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")), false)
		eqv(t, exists(filepath.Join(cwd, ".pi", "tasks", "tasks-s2.json")), false)
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks.json")), []string{"Before", "After"})
	})
	tw(t, f, "never touches the filesystem", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "memory"})
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Ephemeral", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi")), false)
		eqv(t, contains(r.must("TaskList", obj{}), "Ephemeral"), true)
	})
	tw(t, f, "clears tasks on /new, since there is no file to switch away from", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "memory"})
		r, _ := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Ephemeral", "description": "d"})
		r.setSession("s2", true)
		r.fireEvent("session_start", obj{"reason": "new"})
		eqv(t, r.must("TaskList", obj{}), "No tasks found")
	})
	tw(t, f, "keeps tasks across a reload", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "memory"})
		r, _ := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Ephemeral", "description": "d"})
		r.fireEvent("session_start", obj{"reason": "reload"})
		eqv(t, contains(r.must("TaskList", obj{}), "Ephemeral"), true)
	})
	// pi --no-session (and SessionManager.inMemory()) mints a session ID but never a session file. A session
	// task file written for it is orphaned the moment pi exits.
	tw(t, f, "keeps tasks in memory and leaves nothing on disk", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.setSession("s1", false)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.must("TaskCreate", obj{"subject": "Ephemeral", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi")), false)
		eqv(t, contains(r.must("TaskList", obj{}), "Ephemeral"), true)
	})
	tw(t, f, "still writes a session file when the session is persisted", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Durable", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")), true)
	})
	tw(t, f, "does not fall back to a file when a later lifecycle event fires", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.setSession("s1", false)
		r.fireEvent("session_start", obj{"reason": "startup"})
		r.fireEvent("before_agent_start", obj{})
		r.fireEvent("turn_start", obj{})
		r.must("TaskCreate", obj{"subject": "Ephemeral", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi")), false)
	})
	tw(t, f, "resolves a relative path against the session workspace", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "./custom/list.json")
		useConfig(t, obj{"taskScope": "memory"}) // overridden by the env var
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Relative", "description": "d"})
		file := filepath.Join(cwd, "custom", "list.json")
		eqv(t, exists(file), true)
		eqv(t, storedSubjects(t, file)[0], "Relative")
	})
	tw(t, f, "keeps everything in memory when set to off, even in project scope", func(t *testing.T) {
		useEnv(t, "PI_TASKS", "off")
		useConfig(t, obj{"taskScope": "project"})
		cwd := t.TempDir()
		r := startRig(t, cwd)
		r.must("TaskCreate", obj{"subject": "Nowhere", "description": "d"})
		eqv(t, exists(filepath.Join(cwd, ".pi")), false)
	})
	tw(t, f, "wipes an all-completed list on startup, leaving no session file behind", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		file := filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")
		seedStore(t, file, "completed", "completed")
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		eqv(t, exists(file), false)
		eqv(t, len(r.widgetCalls()), 0)
	})
	tw(t, f, "keeps an all-completed list on resume and shows the widget", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		file := filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")
		seedStore(t, file, "completed", "completed")
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "resume"})
		eqv(t, exists(file), true)
		calls := r.widgetCalls()
		if len(calls) == 0 {
			t.Fatal("the widget was not shown")
		}
		eqv(t, calls[0]["key"], "tasks")
		eqv(t, calls[0]["options"].(map[string]any)["placement"], "aboveEditor")
	})
	tw(t, f, "keeps a partially finished list on startup", func(t *testing.T) {
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		file := filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")
		seedStore(t, file, "completed", "pending")
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		eqv(t, exists(file), true)
		eqv(t, len(r.widgetCalls()) > 0, true)
	})
	tw(t, f, "keeps the default scope writing into the workspace", func(t *testing.T) {
		// The compatibility guarantee: upgrading moves nobody's tasks. Asserted against the literal path.
		useConfig(t, obj{})
		useEnv(t, "PI_TASKS", "")
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Default scope", "description": "d"})
		eqv(t, storedSubjects(t, filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")), []string{"Default scope"})
		eqv(t, exists(globalDir(cwd)), false)
	})
	tw(t, f, "keeps a new session's tasks out of the workspace", func(t *testing.T) {
		useConfig(t, obj{"taskScope": "session-global"})
		useEnv(t, "PI_TASKS", "")
		r, cwd := start(t, "startup")
		r.must("TaskCreate", obj{"subject": "Global task", "description": "d"})
		eqv(t, storedSubjects(t, filepath.Join(globalDir(cwd), "tasks-s1.json")), []string{"Global task"})
		eqv(t, exists(filepath.Join(cwd, ".pi", "tasks")), false)
	})
	tw(t, f, "keeps using a session's existing workspace file instead of moving it", func(t *testing.T) {
		// Opting in must not touch data: a session that already lives in the workspace keeps being read and
		// written there.
		useConfig(t, obj{"taskScope": "session-global"})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		inWorkspace := filepath.Join(cwd, ".pi", "tasks", "tasks-s1.json")
		seedStore(t, inWorkspace, "pending")
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "resume"})
		r.must("TaskCreate", obj{"subject": "Written after", "description": "d"})
		eqv(t, storedSubjects(t, inWorkspace), []string{"Task 1", "Written after"})
		eqv(t, exists(globalDir(cwd)), false)
	})
	tw(t, f, "reclaims the global directory once its last session file is gone", func(t *testing.T) {
		// Nothing else ever revisits a workspace whose tasks are gone, so global storage would otherwise
		// grow one empty directory per workspace opened.
		useConfig(t, obj{"taskScope": "session-global"})
		useEnv(t, "PI_TASKS", "")
		cwd := t.TempDir()
		file := filepath.Join(globalDir(cwd), "tasks-s1.json")
		seedStore(t, file, "completed")
		eqv(t, exists(globalDir(cwd)), true)
		r := startRig(t, cwd)
		r.fireEvent("session_start", obj{"reason": "startup"})
		eqv(t, exists(file), false)
		eqv(t, exists(globalDir(cwd)), false)
	})
}

// globalDir is <agent dir>/tasks/sessions/<project key>, where session-global keeps a workspace's files.
func globalDir(cwd string) string {
	key := "--" + strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(strings.TrimLeft(cwd, "/\\")) + "--"
	return filepath.Join(os.Getenv("PIG_CODING_AGENT_DIR"), "tasks", "sessions", key)
}
