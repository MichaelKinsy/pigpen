package schedule_prompt_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	schedule_prompt "github.com/MichaelKinsy/pigpen/schedule-prompt"
)

// Layer-1 tests through the fake PiG host: the extension's wire behavior (events, host calls, files).

type obj = map[string]any

type rig struct {
	*Host
	cwd string
	mu  sync.Mutex
	// entries is what the host answers to sessionRead getEntries.
	entries []any
	session string
}

func startRig(t *testing.T) *rig {
	t.Helper()
	cwd := t.TempDir()
	t.Setenv("PIG_CODING_AGENT_DIR", t.TempDir())
	t.Setenv("PIG_USE_PI_DIRS", "")
	r := &rig{cwd: cwd, session: "s1"}
	r.Host = StartHost(t, schedule_prompt.Extension(), HostOptions{Cwd: cwd, OnCallValue: r.answer})
	return r
}

func (r *rig) answer(method string, args map[string]any) (any, string) {
	if method == "sessionRead" {
		r.mu.Lock()
		defer r.mu.Unlock()
		switch args["method"] {
		case "getSessionId":
			return r.session, ""
		case "getEntries":
			if r.entries == nil {
				return []any{}, ""
			}
			return r.entries, ""
		}
	}
	return map[string]any{}, ""
}

func (r *rig) storePath() string { return filepath.Join(r.cwd, ".pi", "schedule-prompts.json") }

func (r *rig) readStore(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(r.storePath())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (r *rig) call(t *testing.T, params obj) (string, string) {
	t.Helper()
	raw, failure := r.Tool("schedule_prompt", params)
	if failure != "" {
		return "", failure
	}
	var res struct {
		Content string         `json:"content"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
	return res.Content, ""
}

func (r *rig) waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(6 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestToolBeforeSessionStart(t *testing.T) {
	r := startRig(t)
	_, failure := r.call(t, obj{"action": "list"})
	if !strings.Contains(failure, "not ready") {
		t.Fatalf("failure = %q", failure)
	}
}

// An every-second cron job fires: a marker with empty content, then the prompt as a user message with deliverAs followUp, and the
// statistics are written back in the order the original writes them.
func TestCronJobFires(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	text, failure := r.call(t, obj{"action": "add", "name": "tick", "schedule": "* * * * * *", "prompt": "say hi"})
	if failure != "" || !strings.HasPrefix(text, `✓ Created cron job "tick" (`) {
		t.Fatalf("add: %q %q", text, failure)
	}
	r.waitFor(t, "the prompt", func() bool { return len(r.CallsTo("sendUserMessage")) >= 1 })
	if m := r.CallsTo("sendMessage")[0].Args["message"].(map[string]any); m["customType"] != "scheduled_prompt" || len(m["content"].([]any)) != 0 || m["display"] != true {
		t.Fatalf("marker = %v", m)
	}
	u := r.CallsTo("sendUserMessage")[0].Args
	if u["content"] != "say hi" || u["options"].(map[string]any)["deliverAs"] != "followUp" {
		t.Fatalf("user message = %v", u)
	}
	r.waitFor(t, "the statistics", func() bool {
		job := r.readStore(t)["jobs"].([]any)[0].(map[string]any)
		return job["lastStatus"] == "success"
	})
	job := r.readStore(t)["jobs"].([]any)[0].(map[string]any)
	if job["runCount"].(float64) < 1 || job["lastRun"] == nil || job["nextRun"] == nil || job["session"] != "s1" {
		t.Fatalf("job = %v", job)
	}
	var channels []string
	for _, c := range r.CallsTo("events.emit") {
		channels = append(channels, c.Args["channel"].(string))
	}
	if len(channels) == 0 || channels[0] != "cron:change" {
		t.Fatalf("emitted %v", channels)
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
}

// The store file has the original's shape: JSON.stringify(store, null, 2) with jobs then version, a job's keys in creation order.
func TestStoreFileShape(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	if _, failure := r.call(t, obj{"action": "add", "name": "n", "schedule": "0 0 9 * * *", "prompt": "p", "description": "d"}); failure != "" {
		t.Fatal(failure)
	}
	data, _ := os.ReadFile(r.storePath())
	text := string(data)
	for _, want := range []string{"{\n  \"jobs\": [\n    {\n      \"id\": \"", "\"name\": \"n\",\n      \"schedule\": \"0 0 9 * * *\",\n      \"prompt\": \"p\",\n      \"enabled\": true,\n      \"type\": \"cron\",\n      \"createdAt\": \"", "\"runCount\": 0,\n      \"description\": \"d\",\n      \"session\": \"s1\"\n    }\n  ],\n  \"version\": 1\n}"} {
		if !strings.Contains(text, want) {
			t.Fatalf("store file lacks %q:\n%s", want, text)
		}
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatal("the original writes no trailing newline")
	}
	if _, err := os.Stat(r.storePath() + ".tmp"); err == nil {
		t.Fatal("the temporary file was left behind")
	}
	r.Fire("session_shutdown", obj{"reason": "quit"})
}

// Disabled jobs of this session (and unbound ones) are removed on shutdown and on a session switch, never another session's.
func TestAutoCleanup(t *testing.T) {
	seed := `{"jobs":[` +
		`{"id":"mine","name":"mine","schedule":"0 0 * * * *","prompt":"p","enabled":false,"type":"cron","createdAt":"x","runCount":0,"session":"s1"},` +
		`{"id":"foreign","name":"foreign","schedule":"0 0 * * * *","prompt":"p","enabled":false,"type":"cron","createdAt":"x","runCount":0,"session":"s2"},` +
		`{"id":"shared","name":"shared","schedule":"0 0 * * * *","prompt":"p","enabled":false,"type":"cron","createdAt":"x","runCount":0},` +
		`{"id":"live","name":"live","schedule":"0 0 * * * *","prompt":"p","enabled":true,"type":"cron","createdAt":"x","runCount":0,"session":"s1"}],"version":1}`
	ids := func(r *rig, t *testing.T) string {
		var out []string
		for _, j := range r.readStore(t)["jobs"].([]any) {
			out = append(out, j.(map[string]any)["id"].(string))
		}
		return strings.Join(out, ",")
	}
	write := func(r *rig) {
		if err := os.MkdirAll(filepath.Dir(r.storePath()), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(r.storePath(), []byte(seed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("startup keeps them, shutdown sweeps", func(t *testing.T) {
		r := startRig(t)
		write(r)
		r.Fire("session_start", obj{"reason": "startup"})
		if got := ids(r, t); got != "mine,foreign,shared,live" {
			t.Fatalf("startup removed jobs: %s", got)
		}
		r.Fire("session_shutdown", obj{"reason": "quit"})
		if got := ids(r, t); got != "foreign,live" {
			t.Fatalf("after shutdown: %s", got)
		}
	})
	t.Run("a session switch sweeps with the previous storage", func(t *testing.T) {
		r := startRig(t)
		r.Fire("session_start", obj{"reason": "startup"})
		write(r)
		r.Fire("session_start", obj{"reason": "new"})
		if got := ids(r, t); got != "foreign,live" {
			t.Fatalf("after switch: %s", got)
		}
	})
	t.Run("a reload without a previous scheduler sweeps nothing", func(t *testing.T) {
		r := startRig(t)
		write(r)
		r.Fire("session_start", obj{"reason": "reload"})
		if got := ids(r, t); got != "mine,foreign,shared,live" {
			t.Fatalf("reload removed jobs: %s", got)
		}
	})
}

// A stale `running` flag of this session is cleared at the start, one of another session is not.
func TestToolRefusesTheModelModeThroughTheHost(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	_, failure := r.call(t, obj{"action": "add", "schedule": "+10s", "type": "once", "prompt": "p", "model": "haiku"})
	if !strings.Contains(failure, "does not support the per-task model mode") || !strings.HasSuffix(failure, "which runs unchanged in PiG.") {
		t.Fatalf("failure = %q", failure)
	}
	if _, err := os.Stat(r.storePath()); err == nil {
		t.Fatal("a refused call wrote the store")
	}
}


// A change event redraws the widget at once: a job added through the tool shows the widget below the editor without waiting for the
// 30 s refresh.
func TestWidgetFollowsChangeEvents(t *testing.T) {
	r := startRig(t)
	r.Fire("session_start", obj{"reason": "startup"})
	shown := func() int {
		n := 0
		for _, c := range r.CallsTo("ui.setWidget") {
			if c.Args["view"] != nil {
				n++
			}
		}
		return n
	}
	if shown() != 0 {
		t.Fatal("a widget without a job was shown")
	}
	if _, failure := r.call(t, obj{"action": "add", "name": "w", "schedule": "0 0 9 * * *", "prompt": "p"}); failure != "" {
		t.Fatal(failure)
	}
	r.waitFor(t, "the widget", func() bool { return shown() >= 1 })
	r.Fire("session_shutdown", obj{"reason": "quit"})
}
